package raft

import (
	"bytes"
	"fmt"
	"go-map-reduce-and-raft/tester"
	"log"
	"sync"

	"go-map-reduce-and-raft/labgob"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/raftapi"
)

const (
	SnapShotInterval = 10
)

var useRaftStateMachine bool // to plug in another raft besides raft1

type raftServer struct {
	ts          *Test
	me          int
	applyErr    string // from apply channel readers
	lastApplied int
	persister   *tester.Persister

	mu   sync.Mutex
	raft raftapi.Raft
	logs map[int]any // copy of each server's committed entries
}

func newRaftServer(ts *Test, srv int, ends []*labrpc.ClientEnd, persister *tester.Persister, snapshot bool) *raftServer {
	//log.Printf("makeRaftServer %d", srv)
	s := &raftServer{
		ts:        ts,
		me:        srv,
		logs:      map[int]any{},
		persister: persister,
	}
	applyCh := make(chan raftapi.ApplyMsg)
	if !useRaftStateMachine {
		s.raft = Make(ends, srv, persister, applyCh)
	}
	if snapshot {
		snapshot := persister.ReadSnapshot()
		if snapshot != nil && len(snapshot) > 0 {
			// mimic KV server and process snapshot now.
			// ideally Raft should send it up on applyCh...
			err := s.ingestSnap(snapshot, -1)
			if err != "" {
				tester.AnnotateCheckerFailureBeforeExit("failed to ingest snapshot", err)
				ts.t.Fatal(err)
			}
		}
		go s.applierSnap(applyCh)
	} else {
		go s.applier(applyCh)
	}
	return s
}

func (rs *raftServer) Kill() {
	//log.Printf("rs kill %d", rs.me)
	rs.mu.Lock()
	rs.raft = nil // tester will call Kill() on rs.raft
	rs.mu.Unlock()
	if rs.persister != nil {
		// mimic KV server that saves its persistent state in case it
		// restarts.
		raftLog := rs.persister.ReadRaftState()
		snapshot := rs.persister.ReadSnapshot()
		rs.persister.Save(raftLog, snapshot)
	}
}

func (rs *raftServer) GetState() (int, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.raft.GetState()
}

func (rs *raftServer) Raft() raftapi.Raft {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.raft
}

func (rs *raftServer) Logs(i int) (any, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	v, ok := rs.logs[i]
	return v, ok
}

// applier reads message from apply ch and checks that they match the log
// contents
func (rs *raftServer) applier(applyCh chan raftapi.ApplyMsg) {
	for m := range applyCh {
		if m.CommandValid == false {
			// ignore other types of ApplyMsg
		} else {
			errMsg, prevOk := rs.ts.checkLogs(rs.me, m)
			if m.CommandIndex > 1 && prevOk == false {
				errMsg = fmt.Sprintf("server %v apply out of order %v", rs.me, m.CommandIndex)
			}
			if errMsg != "" {
				tester.AnnotateCheckerFailureBeforeExit("apply error", errMsg)
				log.Fatalf("apply error: %v", errMsg)
				rs.applyErr = errMsg
				// keep reading after error so that Raft doesn't block
				// holding locks...
			}
		}
	}
}

// periodically snapshot raft state
func (rs *raftServer) applierSnap(applyCh chan raftapi.ApplyMsg) {
	if rs.raft == nil {
		return // ???
	}

	for m := range applyCh {
		errMsg := ""
		if m.SnapshotValid {
			errMsg = rs.ingestSnap(m.Snapshot, m.SnapshotIndex)
		} else if m.CommandValid {
			if m.CommandIndex != rs.lastApplied+1 {
				errMsg = fmt.Sprintf("server %v apply out of order, expected index %v, got %v", rs.me, rs.lastApplied+1, m.CommandIndex)
			}

			if errMsg == "" {
				var prevOk bool
				errMsg, prevOk = rs.ts.checkLogs(rs.me, m)
				if m.CommandIndex > 1 && prevOk == false {
					errMsg = fmt.Sprintf("server %v apply out of order %v", rs.me, m.CommandIndex)
				}
			}

			rs.lastApplied = m.CommandIndex

			if (m.CommandIndex+1)%SnapShotInterval == 0 {
				w := new(bytes.Buffer)
				e := labgob.NewEncoder(w)
				e.Encode(m.CommandIndex)
				var xLog []any
				for j := 0; j <= m.CommandIndex; j++ {
					xLog = append(xLog, rs.logs[j])
				}
				e.Encode(xLog)
				start := tester.GetAnnotateTimestamp()
				rs.raft.Snapshot(m.CommandIndex, w.Bytes())
				details := fmt.Sprintf(
					"snapshot created after applying the command at index %v",
					m.CommandIndex)
				tester.AnnotateInfoInterval(start, "snapshot created", details)
			}
		} else {
			// Ignore other types of ApplyMsg.
		}
		if errMsg != "" {
			tester.AnnotateCheckerFailureBeforeExit("apply error", errMsg)
			log.Fatalf("apply error: %v", errMsg)
			rs.applyErr = errMsg
			// keep reading after error so that Raft doesn't block
			// holding locks...
		}
	}
}

// returns "" or error string
func (rs *raftServer) ingestSnap(snapshot []byte, index int) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if snapshot == nil {
		tester.AnnotateCheckerFailureBeforeExit("failed to ingest snapshot", "nil snapshot")
		log.Fatalf("nil snapshot")
		return "nil snapshot"
	}
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)
	var lastIncludedIndex int
	var xLog []any
	if d.Decode(&lastIncludedIndex) != nil ||
		d.Decode(&xLog) != nil {
		text := "failed to decode snapshot"
		tester.AnnotateCheckerFailureBeforeExit(text, text)
		log.Fatalf("snapshot decode error")
		return "snapshot Decode() error"
	}
	if index != -1 && index != lastIncludedIndex {
		err := fmt.Sprintf("server %v snapshot doesn't match m.SnapshotIndex", rs.me)
		return err
	}
	rs.logs = map[int]any{}
	for j := 0; j < len(xLog); j++ {
		rs.logs[j] = xLog[j]
	}
	rs.lastApplied = lastIncludedIndex
	return ""
}
