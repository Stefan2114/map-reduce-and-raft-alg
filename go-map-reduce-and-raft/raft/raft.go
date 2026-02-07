package raft

// Make() creates a new raft peer that implements the raft interface.
import (
	"go-map-reduce-and-raft/tester"
	//	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"go-map-reduce-and-raft/labgob"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/raftapi"
)

type NodeState int

const (
	StateFollower  NodeState = 0
	StateCandidate NodeState = 1
	StateLeader    NodeState = 2
)

// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.RWMutex        // TODO: maybe a simple Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *tester.Persister   // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (3A, 3B, 3C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	applyCh chan raftapi.ApplyMsg
	//applyCond      *sync.Cond   // used to wakeup applier goroutine after committing new entries
	//replicatorCond []*sync.Cond // used to signal replicator goroutine to batch replicating entries
	state NodeState

	currentTerm int
	votedFor    int
	// TODO logs        []Entry // the first entry is a dummy entry which contains LastSnapshotTerm, LastSnapshotIndex and nil Command

	commitIndex int
	lastApplied int
	nextIndex   []int
	matchIndex  []int

	electionTimer  *time.Timer
	heartBeatTimer *time.Timer
}

func (rf *Raft) GetState() (int, bool) {

	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.state == StateLeader
}

func (rf *Raft) ChangeState(state NodeState) {
	rf.state = state
}

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
// before you've implemented snapshots, you should pass nil as the
// second argument to persister.Save().
// after you've implemented snapshots, pass the current snapshot
// (or nil if there's not yet a snapshot).
func (rf *Raft) persist() {
	// Your code here (3C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// raftState := w.Bytes()
	// rf.persister.Save(raftState, nil)
}

// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (3C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// how many bytes in Raft's persisted log?
func (rf *Raft) PersistBytes() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.persister.RaftStateSize()
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (3D).

}

type RequestVoteArgs struct {
	// Your data here (3A, 3B).
	Term        int
	CandidateId int
	//LastLogTerm  int TODO
	//LastLogIndex int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

type AppendEntriesArgs struct {
	Term     int // leader’s term [cite: 169]
	LeaderId int // so follower can redirect clients [cite: 170]
	//PrevLogIndex int         // TODO index of log entry immediately preceding new ones [cite: 171]
	//PrevLogTerm  int         // term of prevLogIndex entry [cite: 172]
	//Entries      []LogEntry  // log entries to store (empty for heartbeat) [cite: 173]
	LeaderCommit int // leader’s commitIndex [cite: 173]
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (3A, 3B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	// TODO defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing requestVoteRequest %v and reply requestVoteResponse %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), args, reply)
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v} before processing requestVoteRequest %v and reply requestVoteResponse %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, args, reply)

	if args.Term < rf.currentTerm || (args.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != args.CandidateId) {
		reply.Term, reply.VoteGranted = rf.currentTerm, false
		return
	}

	if args.Term > rf.currentTerm {
		rf.ChangeState(StateFollower)
		rf.currentTerm, rf.votedFor = args.Term, -1
	}

	//if !rf.isLogUpToDate(args.LastLogTerm, args.LastLogIndex) { TODO
	//	reply.Term, reply.VoteGranted = rf.currentTerm, false
	//	return
	//}
	rf.votedFor = args.CandidateId
	rf.electionTimer.Reset(RandomizedElectionTimeout())
	reply.Term, reply.VoteGranted = rf.currentTerm, true
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return. Thus, there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (3B).

	return index, term, isLeader
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) ticker() {
	for rf.killed() == false {

		// Check if a leader election should be started.
		select {
		case <-rf.electionTimer.C:
			rf.mu.Lock()
			rf.ChangeState(StateCandidate)
			rf.currentTerm += 1
			rf.StartElection()
			rf.electionTimer.Reset(RandomizedElectionTimeout())
			rf.mu.Unlock()
		case <-rf.heartBeatTimer.C:
			rf.mu.Lock()
			if rf.state == StateLeader {
				rf.BroadcastHeartbeat(true)
				rf.heartBeatTimer.Reset(StableHeartbeatTimeout())
			}
			rf.mu.Unlock()
		}

		// pause for a random amount of time between 50 and 350
		// milliseconds.
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

func (rf *Raft) StartElection() {
	// request
	args := &RequestVoteArgs{
		Term:        rf.currentTerm,
		CandidateId: rf.me,
	}
	// reply = response *RequestVoteResponse
	DPrintf("{Node %v} starts election with RequestVoteRequest %v", rf.me, args)
	// use Closure
	grantedVotes := 1
	rf.votedFor = rf.me
	rf.persist()
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}

		go func(peer int) {
			reply := new(RequestVoteReply)
			if rf.sendRequestVote(peer, args, reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				DPrintf("{Node %v} receives RequestVoteResponse %v from {Node %v} after sending RequestVoteRequest %v in term %v", rf.me, reply, peer, args, rf.currentTerm)
				if !reply.VoteGranted {
					return
				}
				if rf.currentTerm == args.Term && rf.state == StateCandidate {

					if reply.Term > rf.currentTerm {
						DPrintf("{Node %v} finds a new leader {Node %v} with term %v and steps down in term %v", rf.me, peer, reply.Term, rf.currentTerm)
						rf.ChangeState(StateFollower)
						rf.currentTerm, rf.votedFor = reply.Term, -1
						rf.persist()
						return
					}
					grantedVotes += 1
					if grantedVotes > len(rf.peers)/2 {
						DPrintf("{Node %v} receives majority votes in term %v", rf.me, rf.currentTerm)
						rf.ChangeState(StateLeader)
						rf.BroadcastHeartbeat(true)
					}

				}
			}
		}(peer)
	}
}

// TODO: see where should i really do rf.persist()

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	// TODO defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing AppendEntriesArgs %v and reply AppendEntriesReply %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), args, reply)
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v} before processing AppendEntriesArgs %v and reply AppendEntriesReply %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, args, reply)

	if args.Term < rf.currentTerm {
		reply.Term, reply.Success = rf.currentTerm, false
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
	}

	rf.ChangeState(StateFollower) // TODO see why i need this
	rf.electionTimer.Reset(RandomizedElectionTimeout())

	//if args.PrevLogIndex < rf.getFirstLog().Index {
	//	reply.Term, reply.Success = 0, false
	//	DPrintf("{Node %v} receives unexpected AppendEntriesArgs %v from {Node %v} because prevLogIndex %v < firstLogIndex %v", rf.me, args, args.LeaderId, args.PrevLogIndex, rf.getFirstLog().Index)
	//	return
	//}
	//
	//if !rf.matchLog(args.PrevLogTerm, args.PrevLogIndex) {
	//	reply.Term, reply.Success = rf.currentTerm, false
	//	lastIndex := rf.getLastLog().Index
	//	if lastIndex < args.PrevLogIndex {
	//		reply.ConflictTerm, reply.ConflictIndex = -1, lastIndex+1
	//	} else {
	//		firstIndex := rf.getFirstLog().Index
	//		reply.ConflictTerm = rf.logs[args.PrevLogIndex-firstIndex].Term
	//		index := args.PrevLogIndex - 1
	//		for index >= firstIndex && rf.logs[index-firstIndex].Term == reply.ConflictTerm {
	//			index--
	//		}
	//		reply.ConflictIndex = index
	//	}
	//	return
	//}
	//
	//firstIndex := rf.getFirstLog().Index
	//for index, entry := range args.Entries {
	//	if entry.Index-firstIndex >= len(rf.logs) || rf.logs[entry.Index-firstIndex].Term != entry.Term {
	//		rf.logs = shrinkEntriesArray(append(rf.logs[:entry.Index-firstIndex], args.Entries[index:]...))
	//		break
	//	}
	//}
	//
	//rf.advanceCommitIndexForFollower(args.LeaderCommit)

	reply.Term, reply.Success = rf.currentTerm, true
}

// TODO
//func (rf *Raft) replicateOneRound(peer int) {
//	rf.mu.RLock()
//	if rf.state != StateLeader {
//		rf.mu.RUnlock()
//		return
//	}
//	prevLogIndex := rf.nextIndex[peer] - 1
//	if prevLogIndex < rf.getFirstLog().Index {
//		// only snapshot can catch up
//		request := rf.genInstallSnapshotRequest()
//		rf.mu.RUnlock()
//		response := new(InstallSnapshotResponse)
//		if rf.sendInstallSnapshot(peer, request, response) {
//			rf.mu.Lock()
//			rf.handleInstallSnapshotResponse(peer, request, response)
//			rf.mu.Unlock()
//		}
//	} else {
//		// just entries can catch up
//		request := rf.genAppendEntriesRequest(prevLogIndex)
//		rf.mu.RUnlock()
//		response := new(AppendEntriesResponse)
//		if rf.sendAppendEntries(peer, request, response) {
//			rf.mu.Lock()
//			rf.handleAppendEntriesResponse(peer, request, response)
//			rf.mu.Unlock()
//		}
//	}
//}

func (rf *Raft) BroadcastHeartbeat(isHeartBeat bool) {
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		if isHeartBeat {
			args := &AppendEntriesArgs{
				Term:     rf.currentTerm,
				LeaderId: rf.me,
				//PrevLogIndex: rf.getLastLog().Index, // TODO For 3A, these can be simplified
				//PrevLogTerm:  rf.getLastLog().Term,
				// Entries:      nil, // Empty entries signifies a heartbeat
				LeaderCommit: rf.commitIndex,
			}

			go func(peer int, args *AppendEntriesArgs) {
				reply := &AppendEntriesReply{}
				if ok := rf.sendAppendEntries(peer, args, reply); ok {
					rf.mu.Lock()
					defer rf.mu.Unlock()

					// If we get a higher term, we must step down immediately
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.votedFor = -1
						rf.ChangeState(StateFollower)
						rf.persist()
						rf.electionTimer.Reset(RandomizedElectionTimeout())
						return
					}

					// In Part 3A, you don't need to handle reply.Success yet.
				}
			}(peer, args)
			// need sending at once to maintain leadership
			// go rf.replicateOneRound(peer) TODO
		} else {
			//	// just signal replicator goroutine to send entries in batch
			//	rf.replicatorCond[peer].Signal()
		}
	}
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {

	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)

	return ok

}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *tester.Persister, applyCh chan raftapi.ApplyMsg) raftapi.Raft {
	rf := &Raft{
		peers:     peers,
		persister: persister,
		me:        me,
		dead:      0,
		applyCh:   applyCh,
		//replicatorCond: make([]*sync.Cond, len(peers)), TODO
		state:       StateFollower,
		currentTerm: 0,
		votedFor:    -1,
		// TODO logs:           make([]Entry, 1),
		nextIndex:      make([]int, len(peers)),
		matchIndex:     make([]int, len(peers)),
		heartBeatTimer: time.NewTimer(StableHeartbeatTimeout()),
		electionTimer:  time.NewTimer(RandomizedElectionTimeout())}

	// Your initialization code here (3A, 3B, 3C).

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	//rf.applyCond = sync.NewCond(&rf.mu) // TODO all this lines
	//lastLog := rf.getLastLog()
	//for i := 0; i < len(peers); i++ {
	//	rf.matchIndex[i], rf.nextIndex[i] = 0, lastLog.Index+1
	//	if i != rf.me {
	//		rf.replicatorCond[i] = sync.NewCond(&sync.Mutex{})
	//		// start replicator goroutine to replicate entries in batch
	//		go rf.replicator(i)
	//	}
	//}

	// start ticker goroutine to start elections
	go rf.ticker()
	// TODO start applier goroutine to push committed logs into applyCh exactly once
	// go rf.applier()

	return rf
}

func RandomizedElectionTimeout() time.Duration {
	ms := 400 + rand.Int63()%400
	return time.Duration(ms) * time.Millisecond
}

func StableHeartbeatTimeout() time.Duration {
	return time.Duration(100) * time.Millisecond
}
