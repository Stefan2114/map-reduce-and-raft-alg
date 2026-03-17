package rsm

import (
	"fmt"
	"sync"
	"time"

	"go-map-reduce-and-raft/kvsrv/rpc"
	"go-map-reduce-and-raft/kvtest"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/raft"
	"go-map-reduce-and-raft/raftapi"
	"go-map-reduce-and-raft/tester"
)

var useRaftStateMachine bool // to plug in another raft besided raft1

type Op struct {
	Me  int
	Id  string // Unique ID to match Submit with the applied result
	Req any
}

type result struct {
	id  string
	val any
}

type pendingEntry struct {
	id   string
	term int
	ch   chan result
}

// A server (i.e., ../server.go) that wants to replicate itself calls
// MakeRSM and must implement the StateMachine interface.  This
// interface allows the rsm package to interact with the server for
// server-specific operations: the server must implement DoOp to
// execute an operation (e.g., a Get or Put request), and
// Snapshot/Restore to snapshot and restore the server's state.
type StateMachine interface {
	DoOp(any) any
	Snapshot() []byte
	Restore([]byte)
}

type RSM struct {
	mu           sync.Mutex
	me           int
	rf           raftapi.Raft
	applyCh      chan raftapi.ApplyMsg
	maxRaftState int // snapshot if log grows this big
	sm           StateMachine
	pending      map[int]*pendingEntry
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// The RSM should snapshot when Raft's saved state exceeds maxRaftState bytes,
// in order to allow Raft to garbage-collect its log. if maxRaftState is -1,
// you don't need to snapshot.
//
// MakeRSM() must return quickly, so it should start goroutines for
// any long-running work.
func MakeRSM(servers []*labrpc.ClientEnd, me int, persister *tester.Persister, maxRaftState int, sm StateMachine) *RSM {
	rsm := &RSM{
		me:           me,
		maxRaftState: maxRaftState,
		applyCh:      make(chan raftapi.ApplyMsg),
		sm:           sm,
		pending:      make(map[int]*pendingEntry),
	}
	if !useRaftStateMachine {
		rsm.rf = raft.Make(servers, me, persister, rsm.applyCh)
	}
	go rsm.reader()
	return rsm
}

func (rsm *RSM) Raft() raftapi.Raft {
	return rsm.rf
}

// Submit a command to Raft, and wait for it to be committed.  It
// should return ErrWrongLeader if client should find new leader and
// try again.
func (rsm *RSM) Submit(req any) (rpc.Err, any) {

	id := kvtest.RandValue(8)
	op := Op{Me: rsm.me, Id: id, Req: req}
	ch := make(chan result)
	rsm.mu.Lock()

	index, term, isLeader := rsm.rf.Start(op)
	if !isLeader {
		rsm.mu.Unlock()
		return rpc.ErrWrongLeader, nil
	}

	raft.DPrintf("[RSM %d] Submit id=%s index=%d term=%d req=%T", rsm.me, id, index, term, req)
	rsm.pending[index] = &pendingEntry{id: id, term: term, ch: ch}
	rsm.mu.Unlock()

	defer func() {
		rsm.mu.Lock()
		delete(rsm.pending, index)
		rsm.mu.Unlock()
	}()

	select {
	case res, ok := <-ch:
		if !ok {
			raft.DPrintf("[RSM %d] Submit id=%s index=%d ch closed (shutdown)", rsm.me, id, index)
			return rpc.ErrWrongLeader, nil
		}
		if res.id != id {
			raft.DPrintf("[RSM %d] Submit id=%s index=%d got wrong id=%s (leader changed)", rsm.me, id, index, res.id)
			return rpc.ErrWrongLeader, nil
		}
		raft.DPrintf("[RSM %d] Submit id=%s index=%d SUCCESS", rsm.me, id, index)
		return rpc.OK, res.val
	case <-time.After(10 * time.Second):
		rsm.mu.Lock()
		pending := rsm.dumpPending()
		rsm.mu.Unlock()
		raft.DPrintf("[RSM %d] Submit id=%s index=%d TIMEOUT STUCK - pending map: %v", rsm.me, id, index, pending)
		return rpc.ErrWrongLeader, nil
	}
}

func (rsm *RSM) dumpPending() string {
	var s string
	for idx, e := range rsm.pending {
		s += fmt.Sprintf("idx=%d id=%s term=%d | ", idx, e.id, e.term)
	}
	return s
}

func (rsm *RSM) reader() {
	for msg := range rsm.applyCh {
		if msg.SnapshotValid {
			raft.DPrintf("[RSM %d] reader: snapshot index=%d", rsm.me, msg.SnapshotIndex)
			rsm.mu.Lock()
			rsm.sm.Restore(msg.Snapshot)
			for idx, entry := range rsm.pending {
				if idx <= msg.SnapshotIndex {
					entry.ch <- result{id: ""}
					delete(rsm.pending, idx)
				}
			}
			rsm.mu.Unlock()
			continue
		}

		if !msg.CommandValid {
			raft.DPrintf("[RSM %d] reader: invalid command msg", rsm.me)
			continue
		}

		op, ok := msg.Command.(Op)
		if !ok {
			raft.DPrintf("[RSM %d] reader: command not Op type: %T", rsm.me, msg.Command)
			continue
		}

		raft.DPrintf("[RSM %d] reader: applying index=%d op.id=%s op.Me=%d", rsm.me, msg.CommandIndex, op.Id, op.Me)
		ret := rsm.sm.DoOp(op.Req)

		rsm.mu.Lock()
		if entry, exists := rsm.pending[msg.CommandIndex]; exists {
			raft.DPrintf("[RSM %d] reader: notifying pending index=%d id=%s matches=%v",
				rsm.me, msg.CommandIndex, op.Id, entry.id == op.Id)
			entry.ch <- result{id: op.Id, val: ret}
			delete(rsm.pending, msg.CommandIndex)
		}

		currentTerm, isLeader := rsm.rf.GetState()
		for idx, entry := range rsm.pending {
			if idx < msg.CommandIndex || (!isLeader && entry.term < currentTerm) {
				entry.ch <- result{id: ""} // mismatch -> ErrWrongLeader
				delete(rsm.pending, idx)
			}
		}

		if rsm.maxRaftState != -1 && rsm.rf.PersistBytes() >= rsm.maxRaftState {
			snap := rsm.sm.Snapshot()
			rsm.rf.Snapshot(msg.CommandIndex, snap)
		}

		rsm.mu.Unlock()
	}
	raft.DPrintf("[RSM %d] reader: applyCh closed, waking all pending", rsm.me)
	// applyCh closed: wake up all waiting Submit() calls
	rsm.mu.Lock()
	for _, entry := range rsm.pending {
		close(entry.ch)
	}
	rsm.pending = make(map[int]*pendingEntry)
	rsm.mu.Unlock()
}
