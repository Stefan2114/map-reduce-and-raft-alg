package raft

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"go-map-reduce-and-raft/labgob"
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

	lastIncludedIndex int
	lastIncludedTerm  int

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
	newIndex := rf.getLen()
	newTerm := rf.currentTerm
	entry := Entry{
		Index:   newIndex,
		Term:    newTerm,
		Command: command,
	}
	rf.logs = append(rf.logs, entry)
	rf.persist()
	DPrintf("{Node %v} receives a new command(index: %v, term: %v) to replicate in term %v", rf.me, newIndex, newTerm, rf.currentTerm)
	rf.BroadcastHeartbeat()
	return newIndex, newTerm, true
}

func (rf *Raft) encodeState() []byte {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	if e.Encode(rf.currentTerm) != nil ||
		e.Encode(rf.votedFor) != nil ||
		e.Encode(rf.logs) != nil ||
		e.Encode(rf.lastIncludedIndex) != nil ||
		e.Encode(rf.lastIncludedTerm) != nil {
		panic("failed to encode state")
	}
	return w.Bytes()
}

// save Raft's persistent state to stable storage
func (rf *Raft) persist() {
	rf.persister.Save(rf.encodeState(), rf.persister.ReadSnapshot())
}

func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 {
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm, votedFor, lastIncludedIndex, lastIncludedTerm int
	var logs []Entry

	if d.Decode(&currentTerm) != nil ||
		d.Decode(&votedFor) != nil ||
		d.Decode(&logs) != nil ||
		d.Decode(&lastIncludedIndex) != nil ||
		d.Decode(&lastIncludedTerm) != nil {
		panic("failed to decode state")
	}
	rf.currentTerm = currentTerm
	rf.votedFor = votedFor
	rf.logs = logs
	rf.lastIncludedIndex = lastIncludedIndex
	rf.lastIncludedTerm = lastIncludedTerm

	if lastIncludedIndex > rf.commitIndex {
		rf.commitIndex = lastIncludedIndex
	}
	if lastIncludedIndex > rf.lastApplied {
		rf.lastApplied = lastIncludedIndex
	}
}

func (rf *Raft) PersistBytes() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.persister.RaftStateSize()
}

func (rf *Raft) Snapshot(index int, snapshot []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if index <= rf.lastIncludedIndex || index > rf.commitIndex {
		return
	}
	rf.logs = append([]Entry{}, rf.logs[rf.getPhysicalIndex(index):]...)
	rf.lastIncludedIndex = index
	rf.lastIncludedTerm = rf.getFirstLog().Term
	rf.persister.Save(rf.encodeState(), snapshot)
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
			if rf.killed() {
				rf.mu.Unlock()
				return
			}
		}

		// Check if we need to apply a snapshot first
		// If lastApplied is less than lastIncludedIndex, the state machine is behind the snapshot
		if rf.lastApplied < rf.lastIncludedIndex {
			msg := raftapi.ApplyMsg{
				SnapshotValid: true,
				Snapshot:      rf.persister.ReadSnapshot(),
				SnapshotTerm:  rf.lastIncludedTerm,
				SnapshotIndex: rf.lastIncludedIndex,
			}
			rf.lastApplied = rf.lastIncludedIndex
			rf.mu.Unlock()
			rf.applyCh <- msg
			continue
		}

		start := rf.lastApplied + 1
		limit := rf.commitIndex

		pStart := rf.getPhysicalIndex(start)
		pLimit := rf.getPhysicalIndex(limit)

		// Slice and copy to avoid data races once we unlock [cite: 428]
		entries := make([]Entry, pLimit-pStart+1)
		copy(entries, rf.logs[pStart:pLimit+1])

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
	rf.persist()
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
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing requestVoteRequest %v and reply requestVoteResponse %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), args, reply)

	reply.Term, reply.VoteGranted = rf.currentTerm, false

	if args.Term < rf.currentTerm {
		return
	}
	defer rf.persist()

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
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v} before processing AppendEntriesArgs %v and reply AppendEntriesReply %v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, args, reply)

	reply.Term, reply.Success = rf.currentTerm, false

	if args.Term < rf.currentTerm {
		return
	}
	defer rf.persist()

	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
	}

	rf.state = StateFollower
	rf.resetElectionTimer()

	if hasConflict := rf.handleConsistencyConflict(args, reply); hasConflict {
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

	if args.PrevLogIndex < rf.lastIncludedIndex {
		reply.ConflictIndex = rf.lastIncludedIndex + 1
		reply.ConflictTerm = -1
		return true
	}

	if rf.hasEntryAt(args.PrevLogIndex, args.PrevLogTerm) {
		return false
	}

	if args.PrevLogIndex >= rf.getLen() {
		reply.ConflictIndex = rf.getLen()
		reply.ConflictTerm = -1
		return true
	}

	reply.ConflictTerm = rf.getLog(args.PrevLogIndex).Term
	index := args.PrevLogIndex
	for index > 0 && rf.getLog(index).Term == reply.ConflictTerm {
		index--
	}
	reply.ConflictIndex = index + 1
	return true
}

func (rf *Raft) appendNewEntries(entries []Entry) {
	for i, entry := range entries {

		if entry.Index <= rf.commitIndex {
			continue
		}
		if entry.Index < rf.getLen() {
			// Rule 3: If an existing entry conflicts with a new one (same index
			// but different terms), delete the existing entry and all that follow it
			pIdx := rf.getPhysicalIndex(entry.Index)
			if rf.logs[pIdx].Term != entry.Term {
				rf.logs = rf.logs[:pIdx] // Truncate at physical index
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
		lastIndex := rf.getLen() - 1
		// Rule 5: set commitIndex = min(leaderCommit, index of last new entry)
		if leaderCommit < lastIndex {
			rf.commitIndex = leaderCommit
		} else {
			rf.commitIndex = lastIndex
		}
		rf.applyCond.Broadcast()
	}
}

func (rf *Raft) StartElection() {

	args := rf.genRequestVoteArgs()
	DPrintf("{Node %v} starts election with RequestVoteRequest %v", rf.me, args)
	rf.votedFor = rf.me
	rf.persist()
	grantedVotes := 1
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		go func(peer int) {
			reply := new(RequestVoteReply)
			if ok := rf.sendRequestVote(peer, args, reply); ok {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				DPrintf("{Node %v} receives RequestVoteResponse %v from {Node %v} after sending RequestVoteRequest %v in term %v", rf.me, reply, peer, args, rf.currentTerm)
				if !rf.isStillValidCandidate(args.Term) {
					return
				}
				if isHigher := rf.handleHigherTerm(reply.Term); isHigher {
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

	if rf.nextIndex[peer] <= rf.lastIncludedIndex {

		args := &InstallSnapshotArgs{
			Term:              rf.currentTerm,
			LeaderId:          rf.me,
			LastIncludedIndex: rf.lastIncludedIndex,
			LastIncludedTerm:  rf.lastIncludedTerm,
			Data:              rf.persister.ReadSnapshot(),
		}
		rf.mu.Unlock()

		reply := &InstallSnapshotReply{}
		if ok := rf.peers[peer].Call("Raft.InstallSnapshot", args, reply); ok {
			rf.mu.Lock()
			if isHigher := rf.handleHigherTerm(reply.Term); isHigher {
				rf.mu.Unlock()
				return
			}
			if rf.state == StateLeader && rf.currentTerm == args.Term {
				if rf.matchIndex[peer] < args.LastIncludedIndex {
					rf.matchIndex[peer] = args.LastIncludedIndex
					rf.nextIndex[peer] = rf.matchIndex[peer] + 1
				}
			}
			rf.mu.Unlock()
		}
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

	pPrev := rf.getPhysicalIndex(prevLogIndex)
	entries := make([]Entry, len(rf.logs)-(pPrev+1))
	copy(entries, rf.logs[pPrev+1:])

	return &AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderId:     rf.me,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  rf.logs[pPrev].Term,
		Entries:      entries,
		LeaderCommit: rf.commitIndex,
	}
}

func (rf *Raft) handleAppendEntriesReply(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) {

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if isHigher := rf.handleHigherTerm(reply.Term); isHigher {
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
				if rf.getLog(i).Term == reply.ConflictTerm {
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
	for n := rf.getLen() - 1; n > rf.commitIndex; n-- {
		if rf.getLog(n).Term == rf.currentTerm && rf.countNodesWithLogAt(n) > len(rf.peers)/2 {
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

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
}

type InstallSnapshotReply struct {
	Term int
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {

	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	if args.Term < rf.currentTerm {
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
		rf.persist()
	}

	rf.state = StateFollower
	rf.resetElectionTimer()

	if args.LastIncludedIndex <= rf.lastIncludedIndex {
		return
	}

	if args.LastIncludedIndex < rf.getLen() && rf.getLog(args.LastIncludedIndex).Term == args.LastIncludedTerm {
		// We have the entry. Slice it so that the entry at LastIncludedIndex is at physical index 0
		rf.logs = append([]Entry{}, rf.logs[rf.getPhysicalIndex(args.LastIncludedIndex):]...)
	} else {
		// We don't have it or there's a term mismatch. Reset the log.
		rf.logs = []Entry{{Index: args.LastIncludedIndex, Term: args.LastIncludedTerm}}
	}

	rf.lastIncludedIndex = args.LastIncludedIndex
	rf.lastIncludedTerm = args.LastIncludedTerm

	if args.LastIncludedIndex > rf.commitIndex {
		rf.commitIndex = args.LastIncludedIndex
	}

	rf.persister.Save(rf.encodeState(), args.Data)
	rf.applyCond.Broadcast()
}

func (rf *Raft) getLastLog() Entry {
	return rf.logs[len(rf.logs)-1]
}

func (rf *Raft) getFirstLog() Entry {
	return rf.logs[0]
}

func (rf *Raft) getLog(index int) Entry {
	return rf.logs[rf.getPhysicalIndex(index)]
}

func (rf *Raft) getPhysicalIndex(index int) int {
	return index - rf.lastIncludedIndex
}

func (rf *Raft) getLen() int {
	return rf.lastIncludedIndex + len(rf.logs)
}

func (rf *Raft) hasEntryAt(index int, term int) bool {
	if index < rf.lastIncludedIndex || index >= rf.getLen() {
		return false
	}
	return rf.getLog(index).Term == term
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
	ms := 400 + rand.Int63()%400
	return time.Duration(ms) * time.Millisecond
}

func StableHeartbeatTimeout() time.Duration {
	return time.Duration(100) * time.Millisecond
}
