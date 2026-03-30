package kvraft

import (
	"bytes"
	"sync"
	"sync/atomic"

	"go-map-reduce-and-raft/kvraft/rsm"
	"go-map-reduce-and-raft/kvsrv/rpc"
	"go-map-reduce-and-raft/labgob"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/tester"
)

type VersionedValue struct {
	Value   string
	Version rpc.TVersion
}
type KVServer struct {
	me   int
	dead int32 // set by Kill()
	rsm  *rsm.RSM

	data map[string]VersionedValue
	mu   sync.Mutex
}

func (kv *KVServer) DoOp(req any) any {
	switch r := req.(type) {
	case rpc.GetArgs:
		kv.mu.Lock()
		defer kv.mu.Unlock()
		entry, ok := kv.data[r.Key]
		if !ok {
			return rpc.GetReply{Err: rpc.ErrNoKey}
		}
		return rpc.GetReply{
			Value:   entry.Value,
			Version: entry.Version,
			Err:     rpc.OK,
		}

	case rpc.PutArgs:
		kv.mu.Lock()
		defer kv.mu.Unlock()
		entry, exists := kv.data[r.Key]
		currentVersion := entry.Version
		if exists && currentVersion != r.Version {
			return rpc.PutReply{Err: rpc.ErrVersion}
		}
		if !exists && r.Version != 0 {
			return rpc.PutReply{Err: rpc.ErrVersion}
		}
		kv.data[r.Key] = VersionedValue{
			Value:   r.Value,
			Version: r.Version + 1,
		}
		return rpc.PutReply{Err: rpc.OK}
	}
	return nil
}

func (kv *KVServer) Snapshot() []byte {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.data)
	return w.Bytes()
}

func (kv *KVServer) Restore(data []byte) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var kvData map[string]VersionedValue
	if d.Decode(&kvData) != nil {
		panic("KVServer: failed to decode")
	}
	kv.data = kvData
}

func (kv *KVServer) Get(args *rpc.GetArgs, reply *rpc.GetReply) {

	err, ret := kv.rsm.Submit(*args)
	if err != rpc.OK {
		reply.Err = err
		return
	}
	rep := ret.(rpc.GetReply)
	reply.Value = rep.Value
	reply.Version = rep.Version
	reply.Err = rep.Err
}

func (kv *KVServer) Put(args *rpc.PutArgs, reply *rpc.PutReply) {

	err, ret := kv.rsm.Submit(*args)
	if err != rpc.OK {
		reply.Err = err
		return
	}
	rep := ret.(rpc.PutReply)
	reply.Err = rep.Err
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// StartKVServer() and MakeRSM() must return quickly, so they should
// start goroutines for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, gid tester.Tgid, me int, persister *tester.Persister, maxRaftState int) []tester.IService {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(rsm.Op{})
	labgob.Register(rpc.PutArgs{})
	labgob.Register(rpc.GetArgs{})
	labgob.Register(rpc.GetReply{})
	labgob.Register(rpc.PutReply{})

	kv := &KVServer{
		me:   me,
		data: make(map[string]VersionedValue),
	}

	kv.rsm = rsm.MakeRSM(servers, me, persister, maxRaftState, kv)
	return []tester.IService{kv, kv.rsm.Raft()}
}
