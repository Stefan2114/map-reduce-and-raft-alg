# Go Distributed Systems: MapReduce & Raft

This repository contains my implementation overview of the labs from the [MIT 6.5840 (Spring 2025) Distributed Systems course](http://nil.csail.mit.edu/6.5840/2025/schedule.html). The project focuses on building fault-tolerant, scalable, and linearizable distributed services using Go.

## Academic Integrity & Code Access

To comply with the [MIT 6.5840 Collaboration Policy](http://nil.csail.mit.edu/6.5840/2025/general.html#collaboration), the full source code for these labs is kept in a **private repository**. 

In this public repository, I have provided:
* **Detailed Design Docs**: Explanations of how I handled concurrency, failure recovery, and linearizability.
* **Key Implementation Snippets**: Selected portions of the Coordinator, Raft safety logic, and the RSM layer to showcase my coding style and architectural thinking.

**For Recruiters and Hiring Managers:**
I am more than happy to discuss my implementation in detail during an interview or provide private access to the full source code for review purposes upon request. Please feel free to reach out via [LinkedIn/Email].

## Lab 1: Distributed MapReduce
A simplified version of the Google MapReduce framework. I implemented a coordinated system where a **Coordinator** manages task distribution and a pool of **Workers** executes Map and Reduce functions in parallel.

* **Key Features:**
    * **Fault Tolerance for workers:** The Coordinator monitors worker health and re-assigns tasks if a worker fails or stalls (10s timeout).
    * **Atomic Commit:** Uses temporary files and `os.Rename` to ensure that partially written files from crashed workers are never observed.
    * **Concurrency:** Handles concurrent RPC heartbeats and reports from multiple workers using Go channels and atomic types.

## Lab 2: Key/Value Server & Distributed Lock
A linearizable Key/Value storage system that maintains consistency across unreliable networks. This serves as a foundation for coordination primitives like distributed locks.

* **Key Features:**
    * **At-Most-Once Semantics:** Implemented version-conditioned `Put` operations to handle duplicate RPC requests caused by network retransmissions.
    * **Distributed Lock:** Built a `Lock` library (Acquire/Release) on top of the KV server, using conditional updates to ensure mutual exclusion across different clients.
    * **Unreliable Network Handling:** The `Clerk` (client) automatically retries operations with exponential backoff and handles `ErrMaybe` scenarios to maintain state integrity.

## Lab 3: Raft Consensus Algorithm

In this lab, I implemented **Raft**, a consensus algorithm designed for manageability and understandability. Raft serves as the backbone of a fault-tolerant system by ensuring that all replica servers agree on a sequence of commands (the log), even in the face of machine crashes or network partitions.

The implementation follows the design outlined in the [Extended Raft Paper](https://raft.github.io/raft.pdf).

---

### Decomposition of Consensus

Raft?s primary design principle is **problem decomposition**. It divides the complex problem of distributed consensus into three relatively independent sub-problems:

1.  **Leader Election**: Choosing a new leader when an existing one fails.
2.  **Log Replication**: The leader accepting client commands and forcing other logs to agree with its own.
3.  **Safety**: Ensuring that if any server has applied a particular log entry to its state machine, then no other server may apply a different command for the same index.

---

### Node States and Transitions

At any given time, a Raft node exists in one of three states:

* **Follower**: The initial, passive state. Followers only respond to RPCs from leaders and candidates.
* **Candidate**: A transitional state used during elections. A node becomes a candidate if it hasn't heard from a leader within its election timeout.
* **Leader**: The distinguished node responsible for managing the replicated log and servicing client requests.



---

### Timing and Communication

Communication is handled via **Remote Procedure Calls (RPCs)**. The main idea of the protocol has two primary RPC types:
* **RequestVote**: Initiated by candidates to gather votes during an election.
* **AppendEntries**: Initiated by the leader to replicate log entries and as a heartbeat mechanism.

#### The Role of Timers
* **Heartbeat Timeout**: The leader sends periodic empty `AppendEntries` RPCs to maintain authority and prevent new elections.
* **Election Timeout**: To prevent "split votes" (where multiple nodes become candidates simultaneously and none get a majority), each node chooses a randomized election timeout. This ensures that one node usually times out first and wins the election before others.

---

### Log Replication and Snapshotting

#### Append Entries
When a leader receives a command, it appends it to its log and sends `AppendEntries` RPCs to followers. A log entry is considered **committed** once it is replicated on a majority of servers. Once committed, the leader applies it to its state machine and notifies followers to do the same.

#### Snapshotting (Log Compaction)
To prevent the log from growing indefinitely, I implemented **Snapshotting**. When the log grows long, it occupies space and takes time to replay during recovery.
* **InstallSnapshot RPC**: If a follower is too far behind and the leader has already discarded the necessary log entries, the leader sends the entire snapshot to that follower using RPC.



---

### Failure Scenarios and Resilience

Raft is designed to remain available as long as a majority of servers are operational.

* **Follower Failure**: If a follower crashes, the leader continues to retry `AppendEntries` indefinitely until the follower restarts and catches up.
* **Leader Failure**: If the leader crashes, followers will time out and trigger a new election. Raft ensures that only a node with all committed entries can be elected leader via an "up-to-date" log check during voting.
* **Network Partition**: In a partitioned cluster, the majority side can still elect a leader and commit entries, while the minority side cannot reach consensus. When the partition heals, nodes with lower terms automatically revert to followers and update their logs to match the current leader.

---

### Testing and Simulation

The implementation was verified using the MIT test suite, which simulates various failure modes:

* **Unreliable Networks**: Randomly dropping, reordering, and delaying RPC messages.
* **Churn**: Frequently crashing and restarting servers to ensure persistence of `currentTerm`, `votedFor`, and `log`.
* **Linearizability**: Ensuring that the system behaves like a single, highly reliable state machine despite concurrency and failures. To verify this, the testing suite uses the **Porcupine** library, a fast linearizability checker for Go, to ensure that the trace of operations across the cluster matches a valid sequential execution.


## Lab 4: Fault-Tolerant Key/Value Service

In this lab, I built a replicated key/value storage service that achieves high availability and linearizability. The service is built as a **Replicated State Machine (RSM)** using the Raft library from Lab 3 as the consensus engine.

The full system interaction follows the official [MIT 6.5840 Raft Interaction Diagram](http://nil.csail.mit.edu/6.5840/2024/figs/raft-structure.pdf).

---

### The RSM Layer (Replicated State Machine)

Instead of a monolithic KV server, I implemented a generic **RSM layer**. This layer sits between the application logic and Raft, providing a clean interface for replication.

* **Request Submission**: The `Submit()` function takes a client request, wraps it in an `Op` with a unique ID, and hands it to Raft.
* **The Reader Goroutine**: A background process constantly consumes Raft's `applyCh`. When a command is committed, the Reader executes it via the `StateMachine` interface (`DoOp`) and notifies the waiting `Submit` goroutine.
* **Linearizability**: To ensure strict consistency, the RSM detects leadership changes. If a command is lost because a leader was deposed, the RSM forces the client to retry with the new leader.

---

### Key/Value Service Implementation

The KV Server implements the `StateMachine` interface defined by the RSM.

* **At-Most-Once Semantics**: Building on Lab 2, the server uses versioning to ensure that retried client requests do not cause duplicate executions.
* **Consistent Reads**: Even `Get` requests are put through the Raft log. This ensures the leader is part of a majority and has the most up-to-date data before responding, preventing "stale reads."
* **Clerk (Client) Logic**: The Clerk intelligently tracks the current leader. If a request fails or returns `ErrWrongLeader`, it rotates through the server list until it finds the new coordinator.

---

### Snapshot Integration and Recovery

To maintain high performance over time, I implemented log compaction:

* **Log Trimming**: The RSM monitors the size of the Raft state. When it approaches `maxraftstate`, it calls `Snapshot()` on the KV server.
* **State Persistence**: The combined state (Raft metadata + KV database snapshot) is saved to the `Persister`.
* **Fast Recovery**: Upon reboot, a server doesn't need to replay thousands of log entries. It simply restores the latest snapshot and starts replaying from the last included index.

---

### Failure Handling Scenarios

The service is verified to handle the following conditions:
* **Reliable Progress**: Continuing to service requests as long as a majority ($N/2+1$) of nodes are alive.
* **Partition Recovery**: When a partitioned node rejoins, it automatically receives an `InstallSnapshot` RPC or the missing log entries to synchronize its database.
