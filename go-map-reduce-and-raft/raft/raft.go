package raft

import (
	"fmt"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/raftapi"
	"go-map-reduce-and-raft/tester"
	// "bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
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

func (e Entry) String() string {
	return fmt.Sprintf("{Idx:%d Trm:%d}", e.Index, e.Term)
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

	go rf.ticker()
	go rf.applier()
	return rf
}

func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.state == StateLeader
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
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
	DPrintf("{Node %v} receives a new command(index: %v, term: %v) to replicate in term %v", rf.me, newIndex, newTerm, rf.currentTerm)
	rf.BroadcastHeartbeat()
	return newIndex, newTerm, true
}

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
func (rf *Raft) persist() {
}

// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
}

// how many bytes in Raft's persisted log?
func (rf *Raft) PersistBytes() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.persister.RaftStateSize()
}

func (rf *Raft) ticker() {
	for rf.killed() == false {
		select {
		case <-rf.electionTimer.C:
			rf.mu.Lock()
			rf.becomeCandidate()
			rf.mu.Unlock()

		case <-rf.heartBeatTimer.C:
			rf.mu.Lock()
			if rf.state == StateLeader {
				rf.BroadcastHeartbeat()
				rf.resetHeartbeatTimer()
			}
			rf.mu.Unlock()
		}
	}
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

func (rf *Raft) resetElectionTimer() {
	rf.electionTimer.Reset(RandomizedElectionTimeout())
}

func (rf *Raft) resetHeartbeatTimer() {
	rf.heartBeatTimer.Reset(StableHeartbeatTimeout())
}

func (rf *Raft) becomeCandidate() {

	rf.state = StateCandidate
	rf.currentTerm += 1
	rf.StartElection()
	rf.resetElectionTimer()
}

func (rf *Raft) becomeLeader() {
	if rf.state != StateCandidate {
		return
	}
	rf.state = StateLeader
	lastIndex := rf.getLastLog().Index
	for i := range rf.peers {
		rf.nextIndex[i] = lastIndex + 1
		rf.matchIndex[i] = 0
	}
	rf.BroadcastHeartbeat()
	rf.resetHeartbeatTimer()
}

func (rf *Raft) handleHigherTerm(term int) bool {
	if term > rf.currentTerm {
		rf.currentTerm = term
		rf.votedFor = -1
		rf.state = StateFollower
		rf.persist()
		rf.resetElectionTimer()
		return true
	}
	return false
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
		rf.resetElectionTimer()
	}
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
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
	Term          int
	Success       bool
	ConflictIndex int
	ConflictTerm  int
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
	rf.resetElectionTimer()

	if rf.handleConsistencyConflict(args, reply) {
		return
	}
	rf.appendNewEntries(args.Entries)
	rf.advanceCommitIndex(args.LeaderCommit)
	reply.Success = true
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok

}

func (rf *Raft) handleConsistencyConflict(args *AppendEntriesArgs, reply *AppendEntriesReply) bool {

	if rf.hasEntryAt(args.PrevLogIndex, args.PrevLogTerm) {
		return false
	}

	if args.PrevLogIndex >= len(rf.logs) {
		reply.ConflictIndex = len(rf.logs)
		reply.ConflictTerm = -1
		return true
	}

	reply.ConflictTerm = rf.logs[args.PrevLogIndex].Term
	index := args.PrevLogIndex
	for index > 0 && rf.logs[index].Term == reply.ConflictTerm {
		index--
	}
	reply.ConflictIndex = index + 1
	return true
}

func (rf *Raft) appendNewEntries(entries []Entry) {
	for i, entry := range entries {
		if entry.Index < len(rf.logs) {
			// Rule 3: If an existing entry conflicts with a new one (same index
			// but different terms), delete the existing entry and all that follow it
			if rf.logs[entry.Index].Term != entry.Term {
				rf.logs = rf.logs[:entry.Index]
				rf.logs = append(rf.logs, entries[i:]...)
				break
			}
		} else {
			// Entry is beyond current log length, just append all remaining entries
			rf.logs = append(rf.logs, entries[i:]...)
			break
		}
	}
}

func (rf *Raft) advanceCommitIndex(leaderCommit int) {
	if leaderCommit > rf.commitIndex {
		lastIndex := len(rf.logs) - 1
		// Rule 5: set commitIndex = min(leaderCommit, index of last new entry)
		if leaderCommit < lastIndex {
			rf.commitIndex = leaderCommit
		} else {
			rf.commitIndex = lastIndex
		}
		rf.applyCond.Broadcast()
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
		rf.handleAppendEntriesReply(peer, args, reply)
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
		Entries:      entries,
		LeaderCommit: rf.commitIndex,
	}
}

func (rf *Raft) handleAppendEntriesReply(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) {

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.handleHigherTerm(reply.Term) {
		return
	}
	if rf.state != StateLeader || rf.currentTerm != args.Term {
		return
	}
	if reply.Success {
		// Update indices based on the entries WE SENT, not current log length
		newMatch := args.PrevLogIndex + len(args.Entries)
		if newMatch > rf.matchIndex[peer] {
			rf.matchIndex[peer] = newMatch
			rf.nextIndex[peer] = newMatch + 1
		}
		rf.updateCommitIndex()
	} else {
		//// 3. Handle Failure (Log Inconsistency)
		if reply.ConflictTerm == -1 {
			// Follower log is too short
			rf.nextIndex[peer] = reply.ConflictIndex
		} else {
			// Follower has a term mismatch
			// Optimization: search leader's log for the conflict term
			found := false
			for i := args.PrevLogIndex; i >= 1; i-- {
				if rf.logs[i].Term == reply.ConflictTerm {
					rf.nextIndex[peer] = i + 1
					found = true
					break
				}
			}
			if !found {
				rf.nextIndex[peer] = reply.ConflictIndex
			}
		}
		go rf.replicateToPeer(peer)
	}
}

func (rf *Raft) updateCommitIndex() {
	for n := len(rf.logs) - 1; n > rf.commitIndex; n-- {
		if rf.logs[n].Term == rf.currentTerm && rf.countNodesWithLogAt(n) > len(rf.peers)/2 {
			rf.commitIndex = n
			rf.applyCond.Broadcast()
			break
		}
	}
}

func (rf *Raft) countNodesWithLogAt(index int) int {
	count := 1 // Count myself
	for i, mIndex := range rf.matchIndex {
		if i != rf.me && mIndex >= index {
			count++
		}
	}
	return count
}

func (rf *Raft) StartElection() {

	args := rf.genRequestVoteArgs()
	DPrintf("{Node %v} starts election with RequestVoteRequest %v", rf.me, args)
	rf.votedFor = rf.me
	grantedVotes := 1
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
				if !rf.isStillValidCandidate(args.Term) {
					return
				}
				if rf.handleHigherTerm(reply.Term) {
					DPrintf("{Node %v} finds a new leader {Node %v} with term %v and steps down in term %v", rf.me, peer, reply.Term, rf.currentTerm)
					return
				}

				if reply.VoteGranted {
					grantedVotes++
					if grantedVotes > len(rf.peers)/2 {
						DPrintf("{Node %v} receives majority votes in term %v", rf.me, rf.currentTerm)
						rf.becomeLeader()
					}
				}

			}
		}(peer)
	}
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

func (rf *Raft) isLogUpToDate(candidateTerm int, candidateIndex int) bool {

	lastLog := rf.getLastLog()
	if candidateTerm != lastLog.Term {
		return candidateTerm > lastLog.Term
	}
	return candidateIndex >= lastLog.Index
}

func (rf *Raft) getLastLog() Entry {
	return rf.logs[len(rf.logs)-1]
}

func (rf *Raft) getFirstLog() Entry {
	return rf.logs[0]
}

func (rf *Raft) hasEntryAt(index int, term int) bool {
	return index < len(rf.logs) && rf.logs[index].Term == term
}

func (rf *Raft) isStillValidCandidate(electionTerm int) bool {
	return rf.state == StateCandidate && rf.currentTerm == electionTerm
}

func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func RandomizedElectionTimeout() time.Duration {
	ms := 600 + rand.Int63()%400
	return time.Duration(ms) * time.Millisecond
}

func StableHeartbeatTimeout() time.Duration {
	return time.Duration(100) * time.Millisecond
}
