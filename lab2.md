# Lab 2: Linearizable Key/Value Server & Distributed Lock

In this lab, I implemented a key/value storage service for a single machine and built a distributed lock library on top of it. The primary goal was to ensure **linearizability** and **at-most-once semantics** even when the network is unreliable and drops RPC requests or replies.

---

## Architecture & Design Patterns

The system consists of three main components: a central **KVServer**, a client-side **Clerk**, and a **Lock** library that coordinates multiple clients.

### 1. The Key/Value Server
The server maintains an in-memory map of keys to values. To ensure safety and linearizability, every key is associated with a **version number**.

**Conditional Updates:**
The server only executes a `Put` if the client's provided version matches the current version in the database. This pattern is essential for handling network retries without double-executing state changes.

```go
func (kv *KVServer) Put(args *rpc.PutArgs, reply *rpc.PutReply) {
    kv.mu.Lock()
    defer kv.mu.Unlock()

    current, ok := kv.data[args.Key]
    // ... handling new keys ...
    
    // Only update if versions match
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
```

### 2. The Clerk (Client) with RPC Retries
The Clerk handles the complexity of an unreliable network. If an RPC call fails (returns `false`), the Clerk retries indefinitely.

**At-Most-Once Logic:**
A major challenge occurs when a reply is lost. The client doesn't know if the server executed the request or not. To handle this, the Clerk uses the `ErrVersion` response to determine the outcome:

- If it gets `ErrVersion` on the **first attempt**, the request definitely failed.

- If it gets `ErrVersion` on a **retry**, it returns `ErrMaybe` because a previous attempt might have actually succeeded.

```go
func (ck *Clerk) Put(key, value string, version rpc.TVersion) rpc.Err {
    firstTry := true
    for {
        ok := ck.client.Call(ck.server, "KVServer.Put", &args, &reply)
        if ok {
            if reply.Err == rpc.OK { return rpc.OK }
            if reply.Err == rpc.ErrVersion {
                if firstTry { return rpc.ErrVersion }
                return rpc.ErrMaybe // Previous attempt might have succeeded
            }
        }
        firstTry = false
        time.Sleep(100 * time.Millisecond) // Backoff
    }
}
```

### 3. The Distributed Lock
Using the KV Clerk, I implemented a `Lock` with `Acquire` and `Release` methods. The lock state is stored as a key in the KV server.

**Mutual Exclusion via Versioning:**
To acquire the lock, a client tries to replace the "free" string with its unique `id`, providing the version it just read. This acts as an **atomic Compare-and-Swap (CAS)** operation.

```go
func (lk *Lock) Acquire() {
    for {
        val, ver, err := lk.ck.Get(lk.key)
        if err == rpc.OK && val == "free" {
            // Attempt to claim the lock conditionally
            putErr := lk.ck.Put(lk.key, lk.id, ver)
            if putErr == rpc.OK { return }
        }
        time.Sleep(50 * time.Millisecond) // Spin-lock backoff
    }
}
```

## Key Technical Challenges
- **Linearizability:** Ensuring that every `Get` and `Put` appears to take place instantaneously at some point between its invocation and its response.

- **Unreliable Networks:** Designing the system to be robust against "lost" replies. The versioning system ensures that even if a `Put` is sent twice, it is only applied once.

- **Concurrency:** Using `sync.Mutex` on the server to prevent race conditions when multiple Clerks attempt to update the same key simultaneously.