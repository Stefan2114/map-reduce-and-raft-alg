package raft

import (
	// "bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
	// "go-map-reduce-and-raft/labgob"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/raftapi"
	"go-map-reduce-and-raft/tester"
)

type NodeState int

const (
	StateFollower  NodeState = 0
	StateCandidate NodeState = 1
	StateLeader    NodeState = 2
)

type Entry struct {
	Index   int
	Term    int
	Command interface{}
}

type Raft struct {
	mu        sync.Mutex
	peers     []*labrpc.ClientEnd
	persister *tester.Persister
	me        int
	dead      int32

	applyCh   chan raftapi.ApplyMsg
	applyCond *sync.Cond

	state       NodeState
	currentTerm int
	votedFor    int
	logs        []Entry

	commitIndex int
	lastApplied int
	nextIndex   []int
	matchIndex  []int

	electionTimer  *time.Timer
	heartBeatTimer *time.Timer
}

func Make(peers []*labrpc.ClientEnd, me int,

	persister *tester.Persister, applyCh chan raftapi.ApplyMsg) raftapi.Raft {

	rf := &Raft{
		peers:          peers,
		persister:      persister,
		me:             me,
		dead:           0,
		applyCh:        applyCh,
		state:          StateFollower,
		currentTerm:    0,
		votedFor:       -1,
		logs:           make([]Entry, 1),
		nextIndex:      make([]int, len(peers)),
		matchIndex:     make([]int, len(peers)),
		heartBeatTimer: time.NewTimer(StableHeartbeatTimeout()),
		electionTimer:  time.NewTimer(RandomizedElectionTimeout())}

	rf.readPersist(persister.ReadRaftState())
	rf.applyCond = sync.NewCond(&rf.mu)
	lastLog := rf.getLastLog()
	for i := 0; i < len(peers); i++ {
		rf.matchIndex[i], rf.nextIndex[i] = 0, lastLog.Index+1
	}

	go rf.ticker()
	go rf.applier()
	return rf
}

func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.state == StateLeader
}

func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (3D).
}

func (rf *Raft) Start(command interface{}) (int, int, bool) {

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state != StateLeader {
		return -1, -1, false
	}
	newIndex := len(rf.logs)
	newTerm := rf.currentTerm
	entry := Entry{
		Index:   newIndex,
		Term:    newTerm,
		Command: command,
	}
	rf.logs = append(rf.logs, entry)
	rf.persist() // TODO see if i need this here
	DPrintf("{Node %v} receives a new command(index: %v, term: %v) to replicate in term %v", rf.me, newIndex, newTerm, rf.currentTerm)
	rf.BroadcastHeartbeat()
	return newIndex, newTerm, true
}

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

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogTerm  int
	LastLogIndex int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {

	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing requestVoteRequest %v and reply requestVoteResponse %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), args, reply)

	reply.Term, reply.VoteGranted = rf.currentTerm, false

	if args.Term < rf.currentTerm {
		return
	}

	if args.Term > rf.currentTerm {
		rf.state = StateFollower
		rf.currentTerm, rf.votedFor = args.Term, -1
	}

	upToDate := rf.isLogUpToDate(args.LastLogTerm, args.LastLogIndex)
	if (rf.votedFor == -1 || rf.votedFor == args.CandidateId) && upToDate {
		reply.VoteGranted = true
		rf.votedFor = args.CandidateId
		rf.electionTimer.Reset(RandomizedElectionTimeout())
	}
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) ticker() {
	for rf.killed() == false {
		select {
		case <-rf.electionTimer.C:
			rf.mu.Lock()
			rf.state = StateCandidate
			rf.currentTerm += 1
			rf.StartElection()
			rf.electionTimer.Reset(RandomizedElectionTimeout())
			rf.mu.Unlock()
		case <-rf.heartBeatTimer.C:
			rf.mu.Lock()
			if rf.state == StateLeader {
				rf.BroadcastHeartbeat()
				rf.heartBeatTimer.Reset(StableHeartbeatTimeout())
			}
			rf.mu.Unlock()
		}
	}
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Entry
	LeaderCommit int
}
type AppendEntriesReply struct {
	Term    int
	Success bool
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v} before processing AppendEntriesArgs %v and reply AppendEntriesReply %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, args, reply)

	reply.Term, reply.Success = rf.currentTerm, false

	if args.Term < rf.currentTerm {
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
	}
	rf.state = StateFollower
	rf.electionTimer.Reset(RandomizedElectionTimeout())

	// Rule 2 (Log Matching): Reply false if log doesn't contain an entry at prevLogIndex whose term matches prevLogTerm
	if args.PrevLogIndex >= len(rf.logs) || rf.logs[args.PrevLogIndex].Term != args.PrevLogTerm {
		return
	}

	// Rule 3 & 4: If an existing entry conflicts with a new one... append any new entries not already in log
	for i, entry := range args.Entries {
		if entry.Index < len(rf.logs) {
			// ONLY truncate if there is a term conflict
			if rf.logs[entry.Index].Term != entry.Term {
				rf.logs = rf.logs[:entry.Index]
				rf.logs = append(rf.logs, args.Entries[i:]...)
				break
			}
			// If terms match, do nothing (idempotency)
		} else {
			// Entry is beyond current log length, just append
			rf.logs = append(rf.logs, args.Entries[i:]...)
			break
		}
	}

	// Rule 5: If leaderCommit > commitIndex, set commitIndex = min(leaderCommit, index of last new entry)
	if args.LeaderCommit > rf.commitIndex {
		lastIndex := len(rf.logs) - 1
		if args.LeaderCommit < lastIndex {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = lastIndex
		}
		rf.applyCond.Broadcast()
	}

	reply.Success = true
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok

}

func (rf *Raft) genRequestVoteArgs() *RequestVoteArgs {
	lastLog := rf.getLastLog()
	return &RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: lastLog.Index,
		LastLogTerm:  lastLog.Term,
	}
}

func (rf *Raft) StartElection() {

	args := rf.genRequestVoteArgs()
	DPrintf("{Node %v} starts election with RequestVoteRequest %v", rf.me, args)
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
				if rf.currentTerm != args.Term || rf.state != StateCandidate {
					return
				}
				if reply.Term > rf.currentTerm {
					DPrintf("{Node %v} finds a new leader {Node %v} with term %v and steps down in term %v", rf.me, peer, reply.Term, rf.currentTerm)
					rf.state = StateFollower
					rf.currentTerm, rf.votedFor = reply.Term, -1
					rf.persist()
					return
				}

				if reply.VoteGranted {
					grantedVotes++
					if grantedVotes > len(rf.peers)/2 {
						DPrintf("{Node %v} receives majority votes in term %v", rf.me, rf.currentTerm)
						rf.state = StateLeader
						lastIndex := rf.getLastLog().Index
						for i := range rf.peers {
							rf.nextIndex[i] = lastIndex + 1
							rf.matchIndex[i] = 0
						}
						rf.BroadcastHeartbeat()
					}
				}

			}
		}(peer)
	}
}

func (rf *Raft) genAppendEntriesArgs(prevLogIndex int) *AppendEntriesArgs {

	entries := make([]Entry, len(rf.logs)-(prevLogIndex+1))
	copy(entries, rf.logs[prevLogIndex+1:])

	return &AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderId:     rf.me,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  rf.logs[prevLogIndex].Term,
		Entries:      entries, // TODO when i send a nil?
		LeaderCommit: rf.commitIndex,
	}
}

func (rf *Raft) BroadcastHeartbeat() {

	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}

		go rf.replicateToPeer(peer)
	}
}

func (rf *Raft) replicateToPeer(peer int) {

	rf.mu.Lock()
	if rf.state != StateLeader {
		rf.mu.Unlock()
		return
	}

	prevLogIndex := rf.nextIndex[peer] - 1

	args := rf.genAppendEntriesArgs(prevLogIndex)
	rf.mu.Unlock()

	reply := new(AppendEntriesReply)
	if ok := rf.sendAppendEntries(peer, args, reply); ok {
		rf.mu.Lock()
		rf.handleAppendEntriesReply(peer, args, reply)
		rf.mu.Unlock()
	}
}

func (rf *Raft) handleAppendEntriesReply(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if reply.Term > rf.currentTerm {
		rf.currentTerm = reply.Term
		rf.votedFor = -1
		rf.state = StateFollower
		rf.persist()
		rf.electionTimer.Reset(RandomizedElectionTimeout())
		return
	}
	if rf.state != StateLeader || rf.currentTerm != args.Term {
		return
	}
	if reply.Success {

		newMatch := args.PrevLogIndex + len(args.Entries)
		if newMatch > rf.matchIndex[peer] {
			rf.matchIndex[peer] = newMatch
			rf.nextIndex[peer] = newMatch + 1
		}
		rf.updateCommitIndex()
	} else {
		// 3. Handle Failure (Log Inconsistency)
		rf.nextIndex[peer] = args.PrevLogIndex
		if rf.nextIndex[peer] < 1 {
			rf.nextIndex[peer] = 1
		}
		go rf.replicateToPeer(peer)
	}
}

func (rf *Raft) updateCommitIndex() {
	// Iterate from current commitIndex + 1 up to the last log index
	for n := len(rf.logs) - 1; n > rf.commitIndex; n-- {
		// Safety: Only commit entries from the CURRENT term (Section 5.4.2)
		if rf.logs[n].Term != rf.currentTerm {
			continue
		}

		count := 1 // Count self
		for i, mIndex := range rf.matchIndex {
			if i != rf.me && mIndex >= n {
				count++
			}
		}

		if count > len(rf.peers)/2 {
			rf.commitIndex = n
			rf.applyCond.Broadcast()
			break
		}
	}
}

func (rf *Raft) isLogUpToDate(candidateTerm int, candidateIndex int) bool {

	lastLog := rf.getLastLog()
	if candidateTerm != lastLog.Term {
		return candidateTerm > lastLog.Term
	}
	return candidateIndex >= lastLog.Index
}

func (rf *Raft) matchLog(term int, index int) bool {
	return index < len(rf.logs) && rf.logs[index].Term == term
}

func (rf *Raft) getLastLog() Entry {
	return rf.logs[len(rf.logs)-1]
}

func (rf *Raft) getFirstLog() Entry {
	return rf.logs[0]
}

func (rf *Raft) applier() {

	for !rf.killed() {
		rf.mu.Lock()
		for rf.lastApplied >= rf.commitIndex {
			rf.applyCond.Wait()
		}

		limit := rf.commitIndex
		start := rf.lastApplied + 1
		entries := make([]Entry, limit-start+1)
		copy(entries, rf.logs[start:limit+1])
		rf.mu.Unlock()

		for _, entry := range entries {
			rf.applyCh <- raftapi.ApplyMsg{
				CommandValid: true,
				Command:      entry.Command,
				CommandIndex: entry.Index,
			}
		}

		rf.mu.Lock()
		if limit > rf.lastApplied {
			rf.lastApplied = limit
		}
		rf.mu.Unlock()
	}
}

func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}
func RandomizedElectionTimeout() time.Duration {
	ms := 400 + rand.Int63()%400
	return time.Duration(ms) * time.Millisecond
}

func StableHeartbeatTimeout() time.Duration {
	return time.Duration(100) * time.Millisecond
}
