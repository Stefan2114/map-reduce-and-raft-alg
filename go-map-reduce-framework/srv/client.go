package kvsrv

import (
	"go-map-reduce-framework/srv/kvtest"
	"go-map-reduce-framework/srv/rpc"
	"go-map-reduce-framework/tester"
	"time"
)

type Clerk struct {
	client *tester.Client
	server string
}

func MakeClerk(client *tester.Client, server string) kvtest.IKVClerk {
	ck := &Clerk{client: client, server: server}
	// TODO You may add code here.
	return ck
}

// Get fetches the current value and version for a key.  It returns
// ErrNoKey if the key does not exist. It keeps trying forever in the
// face of all other errors.
//
// You can send an RPC with code like this:
// ok := ck.client.Call(ck.server, "KVServer.Get", &args, &reply)
//
// The types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. Additionally, reply must be passed as a pointer.
func (ck *Clerk) Get(key string) (string, rpc.TVersion, rpc.Err) {

	args := rpc.GetArgs{Key: key}
	reply := rpc.GetReply{}
	for {
		ok := ck.client.Call(ck.server, "KVServer.Get", &args, &reply)
		if ok == true {
			switch reply.Err {
			case rpc.OK:
				return reply.Value, reply.Version, reply.Err
			case rpc.ErrNoKey:
				return "", 0, rpc.ErrNoKey
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TODO
// Put updates key with value only if the version in the
// request matches the version of the key at the server.  If the
// versions numbers don't match, the server should return
// ErrVersion.  If Put receives an ErrVersion on its first RPC, Put
// should return ErrVersion, since the Put was definitely not
// performed at the server. If the server returns ErrVersion on a
// resend RPC, then Put must return ErrMaybe to the application, since
// its earlier RPC might have been processed by the server successfully
// but the response was lost, and the Clerk doesn't know if
// the Put was performed or not.
//
// You can send an RPC with code like this:
// ok := ck.client.Call(ck.server, "KVServer.Put", &args, &reply)
//
// The types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. Additionally, reply must be passed as a pointer.
func (ck *Clerk) Put(key, value string, version rpc.TVersion) rpc.Err {

	args := rpc.PutArgs{Key: key, Value: value, Version: version}
	reply := rpc.PutReply{}
	firstTry := true
	for {
		ok := ck.client.Call(ck.server, "KVServer.Put", &args, &reply)
		if ok {
			if reply.Err == rpc.OK {
				return rpc.OK
			}
			if reply.Err == rpc.ErrVersion {
				if firstTry {
					return rpc.ErrVersion
				}
				return rpc.ErrMaybe
			}
			if reply.Err == rpc.ErrNoKey {
				return rpc.ErrNoKey
			}
		}
		firstTry = false
		time.Sleep(100 * time.Millisecond)
	}
}
