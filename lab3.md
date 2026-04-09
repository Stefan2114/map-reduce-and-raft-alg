# Lab 3: Raft Consensus Algorithm - Deep Dive Implementation

This lab focuses on the complete implementation of the Raft consensus protocol. My solution implements a linearizable, fault-tolerant replicated state machine that ensures safety and progress across a cluster of servers, even in the face of network partitions and machine crashes.

---

## Core Architecture & State Management

The `Raft` struct manages both persistent state (required for recovery after crashes) and volatile state (required for operational progress).

```go
type Raft struct {
    mu        sync.RWMutex
    peers     []*labrpc.ClientEnd
    persister *tester.Persister
    me        int

    // Persistent state
    state       NodeState
    currentTerm int
    votedFor    int
    logs        []Entry

    // Volatile state
    commitIndex int
    lastApplied int
    nextIndex   []int
    matchIndex  []int

    // Snapshot metadata
    lastIncludedIndex int
    lastIncludedTerm  int

    // Background workers
    applyCond      *sync.Cond
    replicatorCond []*sync.Cond
    electionTimer  *time.Timer
    heartBeatTimer *time.Timer
}
```

## Leader Election Logic
Raft uses randomized timeouts to trigger elections. When a node becomes a **Candidate**, it increments its term and requests votes from all peers.

```go
func (rf *Raft) startElection() {
    rf.votedFor = rf.me
    rf.persist()

    args := rf.genRequestVoteArgs()
    grantedVotes := 1

    for peer := range rf.peers {
        if peer == rf.me { continue }
        go rf.requestVoteFromPeer(peer, args, &grantedVotes)
    }
}

func (rf *Raft) requestVoteFromPeer(peer int, args *RequestVoteArgs, grantedVotes *int) {
    reply := new(RequestVoteReply)
    if ok := rf.sendRequestVote(peer, args, reply); !ok { return }

    rf.mu.Lock()
    defer rf.mu.Unlock()

    if !rf.isStillValidCandidate(args.Term) { return }
    if rf.handleHigherTerm(reply.Term) { return }

    if reply.VoteGranted {
        *grantedVotes++
        if *grantedVotes == (len(rf.peers)/2 + 1) {
            rf.becomeLeader()
        }
    }
}
```

## Log Replication & Persistence
The Leader manages replication via `AppendEntries`. I implemented a dedicated `replicator` goroutine for each peer that waits on a condition variable to trigger replication.

### Consistency Check and Conflict Resolution

If a follower's log is inconsistent, the leader must find the last shared entry. I implemented an optimized conflict search to back up `nextIndex` by entire terms rather than single entries.

```go
func (rf *Raft) handleConsistencyConflict(args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
    if args.PrevLogIndex < rf.lastIncludedIndex {
        reply.ConflictIndex = rf.lastIncludedIndex + 1
        reply.ConflictTerm = -1
        return true
    }
    if rf.hasEntryAt(args.PrevLogIndex, args.PrevLogTerm) { return false }

    if args.PrevLogIndex >= rf.getLen() {
        reply.ConflictIndex = rf.getLen()
        reply.ConflictTerm = -1
        return true
    }

    reply.ConflictTerm = rf.getLog(args.PrevLogIndex).Term
    index := args.PrevLogIndex
    for index > rf.lastIncludedIndex && rf.getLog(index).Term == reply.ConflictTerm {
        index--
    }
    reply.ConflictIndex = index + 1
    return true
}
```

### Persistence

Persistence ensures that a node can recover its state after a crash.

```go
func (rf *Raft) persist() {
    w := new(bytes.Buffer)
    e := labgob.NewEncoder(w)
    e.Encode(rf.currentTerm)
    e.Encode(rf.votedFor)
    e.Encode(rf.logs)
    e.Encode(rf.lastIncludedIndex)
    e.Encode(rf.lastIncludedTerm)
    rf.persister.Save(w.Bytes(), rf.persister.ReadSnapshot())
}
```

## Log Compaction (Snapshotting)
When the log becomes too large, the service creates a snapshot. Raft must then truncate the log and adjust its indexing logic to account for the `lastIncludedIndex`.

```go
func (rf *Raft) Snapshot(index int, snapshot []byte) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    if index <= rf.lastIncludedIndex || index > rf.commitIndex { return }

    // Truncate log and update offset
    rf.logs = append([]Entry{}, rf.logs[rf.getPhysicalIndex(index):]...)
    rf.lastIncludedIndex = index
    rf.lastIncludedTerm = rf.getFirstLog().Term
    rf.persist() // Save truncated log and snapshot
}
```

### InstallSnapshot RPC

If a follower is so far behind that its `nextIndex` has been discarded by the leader's snapshot, the leader sends an `InstallSnapshot` RPC.

```go
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    if args.Term < rf.currentTerm { return }
    rf.handleHigherTerm(args.Term)
    rf.resetElectionTimer()

    if args.LastIncludedIndex <= rf.lastIncludedIndex { return }

    // Truncate existing log or reset if completely superseded
    rf.truncateLogWithSnapshot(args.LastIncludedIndex, args.LastIncludedTerm)
    rf.lastIncludedIndex = args.LastIncludedIndex
    rf.lastIncludedTerm = args.LastIncludedTerm

    rf.persister.Save(rf.encodeState(), args.Data)
    rf.signalApplier()
}
```

## Background Workers

### The Applier

The `applier` goroutine monitors the `commitIndex` and pushes committed commands (or snapshots) to the `applyCh` so the upper-level service can execute them.

```go
func (rf *Raft) applier() {
    for !rf.killed() {
        rf.mu.Lock()
        for rf.lastApplied >= rf.commitIndex {
            rf.applyCond.Wait()
        }
        
        if rf.lastApplied < rf.lastIncludedIndex {
            // Apply snapshot to service
            rf.applyCh <- raftapi.ApplyMsg{SnapshotValid: true, Snapshot: rf.persister.ReadSnapshot()}
            rf.lastApplied = rf.lastIncludedIndex
        } else {
            // Apply log entries to service
            // ... copy entries and send to applyCh ...
        }
        rf.mu.Unlock()
    }
}
```

### Replicator Goroutines

Each peer has a dedicated **replicator** that handles log synchronization and snapshot installation in parallel, ensuring that slow or partitioned peers do not block the leader.

```go
func (rf *Raft) replicator(peer int) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	for !rf.killed() {
		for !rf.needsReplication(peer) {
			rf.replicatorCond[peer].Wait()
		}
		rf.mu.Unlock()
		rf.replicateToPeer(peer)
		rf.mu.Lock()
	}
}

func (rf *Raft) needsReplication(peer int) bool {
	return rf.state == StateLeader && rf.matchIndex[peer] < rf.getLastLog().Index
}
```

## Testing Environment
The MIT 6.5840 testing framework provides a simulated environment that is significantly more challenging than a standard production network:

- **Network Unreliability**: The `labrpc` framework simulates packet loss, reordering, and extreme delays. This tests the implementation's ability to handle retries and stale RPCs.

- **Partitions**: The tester creates various partition topologies (e.g., 2-3 split, ring partitions) to ensure that only the majority can progress and that the minority correctly synchronizes after the partition heals.

- **Crash Recovery**: Nodes are killed and restarted at random intervals. This verifies that the `persist()` and `readPersist()` logic correctly restores the state machine to a consistent point.

- **Concurrency Stress**: By running multiple Raft instances in the same process with shared channels and global timers, the tests expose race conditions that only appear under high load.