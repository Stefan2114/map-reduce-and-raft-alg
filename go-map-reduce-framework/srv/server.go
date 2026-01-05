package kvsrv

import (
	"go-map-reduce-framework/srv/labrpc"
	"go-map-reduce-framework/srv/rpc"
	"go-map-reduce-framework/tester"
	"log"
	"sync"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type KVData struct {
	Value   string
	Version rpc.TVersion
}

type KVServer struct {
	mu sync.Mutex
	// Store data as a map of key -> (value, version)
	data map[string]KVData
}

func MakeKVServer() *KVServer {
	kv := &KVServer{}
	kv.data = make(map[string]KVData)
	return kv
}

// Get returns the value and version for args.Key, if args.Key
// exists. Otherwise, Get returns ErrNoKey.
func (kv *KVServer) Get(args *rpc.GetArgs, reply *rpc.GetReply) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	val, ok := kv.data[args.Key]
	if !ok {
		reply.Err = rpc.ErrNoKey
		return
	}

	reply.Value = val.Value
	reply.Version = val.Version
	reply.Err = rpc.OK
}

// TODO
// Update the value for a key if args.Version matches the version of
// the key on the server. If versions don't match, return ErrVersion.
// If the key doesn't exist, Put installs the value if the
// args.Version is 0, and returns ErrNoKey otherwise.
func (kv *KVServer) Put(args *rpc.PutArgs, reply *rpc.PutReply) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	current, ok := kv.data[args.Key]

	if !ok {
		if args.Version == 0 {
			// Initialize new key
			kv.data[args.Key] = KVData{
				Value:   args.Value,
				Version: 1,
			}
			reply.Err = rpc.OK
		} else {
			reply.Err = rpc.ErrNoKey
		}
		return
	}

	if current.Version == args.Version {
		kv.data[args.Key] = KVData{
			Value:   args.Value,
			Version: current.Version + 1,
		}
		reply.Err = rpc.OK
	} else {
		reply.Err = rpc.ErrVersion
	}
}

// You can ignore Kill() for this lab
func (kv *KVServer) Kill() {
}

// You can ignore all arguments; they are for replicated KVservers
func StartKVServer(ends []*labrpc.ClientEnd, gid tester.Tgid, srv int, persister *tester.Persister) []tester.IService {
	kv := MakeKVServer()
	return []tester.IService{kv}
}
