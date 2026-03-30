package kvraft

import (
	"time"

	"go-map-reduce-and-raft/kvsrv/rpc"
	"go-map-reduce-and-raft/kvtest"
	"go-map-reduce-and-raft/tester"
)

type Clerk struct {
	client     *tester.Client
	servers    []string
	lastLeader int
}

func MakeClerk(client *tester.Client, servers []string) kvtest.IKVClerk {
	ck := &Clerk{client: client, servers: servers, lastLeader: 0}
	return ck
}

// Get fetches the current value and version for a key.  It returns
// ErrNoKey if the key does not exist. It keeps trying forever in the
// face of all other errors.
//
// You can send an RPC to server i with code like this:
// ok := ck.client.Call(ck.servers[i], "KVServer.Get", &args, &reply)
//
// The types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. Additionally, reply must be passed as a pointer.
func (ck *Clerk) Get(key string) (string, rpc.TVersion, rpc.Err) {
	args := &rpc.GetArgs{Key: key}
	server := ck.lastLeader
	tried := 0
	for {
		reply := &rpc.GetReply{}
		ok := ck.client.Call(ck.servers[server], "KVServer.Get", args, reply)
		if ok && reply.Err != rpc.ErrWrongLeader {
			ck.lastLeader = server
			return reply.Value, reply.Version, reply.Err
		}
		tried++
		server = (server + 1) % len(ck.servers)
		if tried%len(ck.servers) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// Put updates key with value only if the version in the
// request matches the version of the key at the server.  If the
// versions numbers don't match, the server should return
// ErrVersion.  If Put receives an ErrVersion on its first RPC, Put
// should return ErrVersion, since the Put was definitely not
// performed at the server. If the server returns ErrVersion on a
// resend RPC, then Put must return ErrMaybe to the application, since
// its earlier RPC might have been processed by the server successfully
// but the response was lost, and the the Clerk doesn't know if
// the Put was performed or not.
//
// You can send an RPC to server i with code like this:
// ok := ck.client.Call(ck.servers[i], "KVServer.Put", &args, &reply)
//
// The types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. Additionally, reply must be passed as a pointer.
func (ck *Clerk) Put(key string, value string, version rpc.TVersion) rpc.Err {
	args := &rpc.PutArgs{Key: key, Value: value, Version: version}
	server := ck.lastLeader
	firstAttempt := true
	tried := 0
	for {
		reply := &rpc.PutReply{}
		ok := ck.client.Call(ck.servers[server], "KVServer.Put", args, reply)
		if ok {
			switch reply.Err {
			case rpc.OK:
				ck.lastLeader = server
				return rpc.OK
			case rpc.ErrVersion:
				ck.lastLeader = server
				if firstAttempt {
					return rpc.ErrVersion
				}
				// On a retry, version mismatch might mean our earlier Put succeeded
				return rpc.ErrMaybe
			case rpc.ErrWrongLeader:
				firstAttempt = false
				// try next server
			default:
				firstAttempt = false
			}
		} else {
			firstAttempt = false
		}
		tried++
		server = (server + 1) % len(ck.servers)
		if tried%len(ck.servers) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
}
