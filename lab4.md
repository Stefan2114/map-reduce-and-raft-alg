# Lab 4: Fault-Tolerant Key/Value Service

In this lab, I built a fault-tolerant key/value storage service by layering a Key/Value server on top of my Raft implementation. This architecture uses a **Replicated State Machine (RSM)** approach to ensure that as long as a majority of servers are operational and can communicate, the service remains available and linearizable.



---

## Replicated State Machine (RSM) Layer

The RSM acts as a generic glue layer between the application (KV Server) and the consensus protocol (Raft). It abstracts the complexities of waiting for log commitment and handling leadership changes.

### 1. RSM Structure and State Machine Interface
The `StateMachine` interface allows the RSM to execute operations and manage snapshots without knowing the internal details of the KV server.

```go
type StateMachine interface {
    DoOp(any) any
    Snapshot() []byte
    Restore([]byte)
}

type RSM struct {
    rf           raftapi.Raft
    applyCh      chan raftapi.ApplyMsg
    sm           StateMachine
    pending      map[int]*pendingEntry // index -> wait channel
    lastApplied  int
}
```

### 2. Request Submission
When the KV server calls `Submit`, the RSM starts agreement in Raft and creates a "pending entry" to wait for the result from the background applier.

```go
func (rsm *RSM) Submit(req any) (rpc.Err, any) {
    op := Op{Id: kvtest.RandValue(8), Req: req}
    ch := make(chan result)

    index, term, isLeader := rsm.rf.Start(op)
    if !isLeader {
        return rpc.ErrWrongLeader, nil
    }

    rsm.mu.Lock()
    rsm.pending[index] = &pendingEntry{id: op.Id, term: term, ch: ch}
    rsm.mu.Unlock()

    select {
    case res := <-ch:
        if res.id != op.Id { return rpc.ErrWrongLeader, nil }
        return rpc.OK, res.val
    case <-time.After(10 * time.Second):
        return rpc.ErrWrongLeader, nil
    }
}
```

### 3. The Reader Goroutine (Expanded Implementation)

The `reader` goroutine is the heart of the RSM. It continuously processes messages from Raft's `applyCh`. Because Raft might send either a committed command or a full snapshot (if a node was partitioned and fell behind), the reader must handle both scenarios to keep the local State Machine consistent.

```go
func (rsm *RSM) reader() {
    for msg := range rsm.applyCh {
        if msg.SnapshotValid {
            rsm.handleSnapshot(msg)
        } else if msg.CommandValid {
            rsm.handleCommand(msg)
        }
    }
    rsm.cleanup()
}

func (rsm *RSM) handleCommand(msg raftapi.ApplyMsg) {
    op, ok := msg.Command.(Op)
    if !ok { return }

    rsm.mu.Lock()
    // Ignore commands that we have already applied (idempotency check)
    if msg.CommandIndex <= rsm.lastApplied {
        rsm.mu.Unlock()
        return
    }
    rsm.lastApplied = msg.CommandIndex
    rsm.mu.Unlock()

    // Execute the operation on the KV database
    resultVal := rsm.sm.DoOp(op.Req)

    rsm.mu.Lock()
    // notifyPending: If this server is the leader, find the goroutine 
    // waiting for this index and send it the resultVal.
    rsm.notifyPending(msg.CommandIndex, op.Id, resultVal)
    
    // Check if the Raft log has grown too large and needs a snapshot
    rsm.checkSnapshot(msg.CommandIndex)
    rsm.mu.Unlock()
}

func (rsm *RSM) notifyPending(index int, id string, val any) {
    _, isLeader := rsm.rf.GetState()
    entry, exists := rsm.pending[index]

    if exists {
        // Only wake up the waiting Submit() if we are still leader 
        // AND the command ID matches what was originally submitted.
        if isLeader && entry.id == id {
            entry.ch <- result{id: id, val: val}
        } else {
            // Signal a retry if leadership changed or log was overwritten
            entry.ch <- result{id: ""} 
        }
        delete(rsm.pending, index)
    }
    // Clean up any other pending entries that are now outdated
    rsm.notifyOutdated(index) 
}
```

## Key/Value Server Implementation
The `KVServer` maintains the actual database and implements the `DoOp` logic required by the RSM.

### 1. Execution Logic
To ensure linearizability, `Put` operations are version-conditioned, ensuring that retried client requests do not cause duplicate state changes.

```go
func (kv *KVServer) DoOp(req any) any {
    switch r := req.(type) {
    case rpc.GetArgs:
        kv.mu.Lock()
        defer kv.mu.Unlock()
        entry, ok := kv.data[r.Key]
        if !ok { return rpc.GetReply{Err: rpc.ErrNoKey} }
        return rpc.GetReply{Value: entry.Value, Version: entry.Version, Err: rpc.OK}

    case rpc.PutArgs:
        kv.mu.Lock()
        defer kv.mu.Unlock()
        // ... Version check logic ...
        kv.data[r.Key] = VersionedValue{Value: r.Value, Version: r.Version + 1}
        return rpc.PutReply{Err: rpc.OK}
    }
    return nil
}
```

### 2. Snapshots and Recovery
The server can serialize its entire state into a byte array. The RSM triggers this when the Raft log exceeds `maxRaftState`.

```go
func (kv *KVServer) Snapshot() []byte {
    kv.mu.Lock()
    defer kv.mu.Unlock()
    w := new(bytes.Buffer)
    e := labgob.NewEncoder(w)
    e.Encode(kv.data)
    return w.Bytes()
}
```

## The Clerk (Client)
The Clerk acts as the gateway for the application. Its primary responsibility is to find the current Raft leader among the cluster and handle "at-most-once" delivery semantics.

### Clerk Structure and Retries
The Clerk keeps track of the `lastLeader` to optimize subsequent requests. If a request fails or the server returns `ErrWrongLeader`, the Clerk rotates to the next server in the list.

```go
type Clerk struct {
    client     *tester.Client
    servers    []string
    lastLeader int
}

func (ck *Clerk) Put(key string, value string, version rpc.TVersion) rpc.Err {
    args := &rpc.PutArgs{Key: key, Value: value, Version: version}
    server := ck.lastLeader
    firstAttempt := true
    tried := 0

    for {
        reply := &rpc.PutReply{}
        // RPC call to a specific KVServer
        ok := ck.client.Call(ck.servers[server], "KVServer.Put", args, reply)
        
        if ok {
            switch reply.Err {
            case rpc.OK:
                ck.lastLeader = server
                return rpc.OK
            case rpc.ErrVersion:
                ck.lastLeader = server
                // Linearizability logic: if it's not the first attempt,
                // the server might have applied a previous retry that had a lost reply.
                if firstAttempt {
                    return rpc.ErrVersion
                }
                return rpc.ErrMaybe
            case rpc.ErrWrongLeader:
                // Move to next server in the cluster
            }
        }
        
        firstAttempt = false
        tried++
        server = (server + 1) % len(ck.servers)
        
        // Small backoff if we've cycled through all servers without luck
        if tried % len(ck.servers) == 0 {
            time.Sleep(20 * time.Millisecond)
        }
    }
}

func (ck *Clerk) Get(key string) (string, rpc.TVersion, rpc.Err) {
    args := &rpc.GetArgs{Key: key}
    server := ck.lastLeader
    for {
        reply := &rpc.GetReply{}
        ok := ck.client.Call(ck.servers[server], "KVServer.Get", args, reply)
        
        if ok && reply.Err != rpc.ErrWrongLeader {
            ck.lastLeader = server
            return reply.Value, reply.Version, reply.Err
        }
        
        server = (server + 1) % len(ck.servers)
        time.Sleep(20 * time.Millisecond)
    }
}
```

## Failure and Reliability Handling
The implementation is designed to handle several critical distributed systems failure modes:

- **Stale Reads**: By routing `Get` requests through the Raft log, I ensure that the server is part of a majority partition before responding, preventing the service from returning stale data from a partitioned ex-leader.

- **Leader Churn**: If a leader crashes after submitting to Raft but before committing, the RSM detects the term change and forces the client to retry.

- **Network Partitions**: During a partition, the minority side will fail to reach consensus in Raft, while the majority side continues to function correctly. Once the partition heals, the lagging nodes catch up via `AppendEntries` or `InstallSnapshot`.