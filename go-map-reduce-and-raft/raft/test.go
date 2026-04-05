package raft

import (
	"fmt"
	//log
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/raftapi"
	"go-map-reduce-and-raft/tester"
)

type Test struct {
	*tester.Config
	t *testing.T
	n int
	g *tester.ServerGrp

	finished int32

	mu       sync.Mutex
	servers  []*raftServer
	maxIndex int
	snapshot bool
}

func makeTest(t *testing.T, n int, reliable bool, snapshot bool) *Test {
	ts := &Test{
		t:        t,
		n:        n,
		servers:  make([]*raftServer, n),
		snapshot: snapshot,
	}
	ts.Config = tester.MakeConfig(t, n, reliable, ts.makeServer)
	ts.Config.SetLongDelays(true)
	ts.g = ts.Group(tester.GRP0)
	return ts
}

func (ts *Test) cleanup() {
	atomic.StoreInt32(&ts.finished, 1)
	ts.End()
	ts.Config.Cleanup()
	ts.CheckTimeout()
}

func (ts *Test) makeServer(ends []*labrpc.ClientEnd, grp tester.Tgid, srv int, persister *tester.Persister) []tester.IService {
	s := newRaftServer(ts, srv, ends, persister, ts.snapshot)
	ts.mu.Lock()
	ts.servers[srv] = s
	ts.mu.Unlock()
	return []tester.IService{s, s.raft}
}

func (ts *Test) restart(i int) {
	ts.g.StartServer(i) // which will call makeServer to make a new server
	ts.Group(tester.GRP0).ConnectAll()
}

func (ts *Test) checkOneLeader() int {
	tester.AnnotateCheckerBegin("checking for a single leader")
	for iters := 0; iters < 10; iters++ {
		ms := 450 + (rand.Int63() % 100)
		time.Sleep(time.Duration(ms) * time.Millisecond)

		leaders := make(map[int][]int)
		for i := 0; i < ts.n; i++ {
			if ts.g.IsConnected(i) {
				if term, leader := ts.servers[i].GetState(); leader {
					leaders[term] = append(leaders[term], i)
				}
			}
		}

		lastTermWithLeader := -1
		for term, leaders := range leaders {
			if len(leaders) > 1 {
				details := fmt.Sprintf("multiple leaders in term %v = %v", term, leaders)
				tester.AnnotateCheckerFailure("multiple leaders", details)
				ts.Fatalf("term %d has %d (>1) leaders", term, len(leaders))
			}
			if term > lastTermWithLeader {
				lastTermWithLeader = term
			}
		}

		if len(leaders) != 0 {
			details := fmt.Sprintf("leader in term %v = %v",
				lastTermWithLeader, leaders[lastTermWithLeader][0])
			tester.AnnotateCheckerSuccess(details, details)
			return leaders[lastTermWithLeader][0]
		}
	}
	details := fmt.Sprintf("unable to find a leader")
	tester.AnnotateCheckerFailure("no leader", details)
	ts.Fatalf("expected one leader, got none")
	return -1
}

func (ts *Test) checkTerms() int {
	tester.AnnotateCheckerBegin("checking term agreement")
	term := -1
	for i := 0; i < ts.n; i++ {
		if ts.g.IsConnected(i) {
			xterm, _ := ts.servers[i].GetState()
			if term == -1 {
				term = xterm
			} else if term != xterm {
				details := fmt.Sprintf("node ids -> terms = { %v -> %v; %v -> %v }",
					i-1, term, i, xterm)
				tester.AnnotateCheckerFailure("term disagreed", details)
				ts.Fatalf("servers disagree on term")
			}
		}
	}
	details := fmt.Sprintf("term = %v", term)
	tester.AnnotateCheckerSuccess("term agreed", details)
	return term
}

func (ts *Test) checkLogs(i int, m raftapi.ApplyMsg) (string, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	errMsg := ""
	v := m.Command
	me := ts.servers[i]
	for j, rs := range ts.servers {
		if old, oldOk := rs.Logs(m.CommandIndex); oldOk && old != v {
			//log.Printf("%v: log %v; server %v\n", i, me.logs, rs.logs)
			// some server has already committed a different value for this entry!
			errMsg = fmt.Sprintf("commit index=%v server=%v %v != server=%v %v",
				m.CommandIndex, i, m.Command, j, old)
		}
	}
	_, prevOk := me.logs[m.CommandIndex-1]
	me.logs[m.CommandIndex] = v
	if m.CommandIndex > ts.maxIndex {
		ts.maxIndex = m.CommandIndex
	}
	return errMsg, prevOk
}

// check that none of the connected servers
// thinks it is the leader.
func (ts *Test) checkNoLeader() {
	tester.AnnotateCheckerBegin("checking no unexpected leader among connected servers")
	for i := 0; i < ts.n; i++ {
		if ts.g.IsConnected(i) {
			_, isLeader := ts.servers[i].GetState()
			if isLeader {
				details := fmt.Sprintf("leader = %v", i)
				tester.AnnotateCheckerFailure("unexpected leader found", details)
				ts.Fatalf(details)
			}
		}
	}
	tester.AnnotateCheckerSuccess("no unexpected leader", "no unexpected leader")
}

func (ts *Test) checkNoAgreement(index int) {
	text := fmt.Sprintf("checking no unexpected agreement at index %v", index)
	tester.AnnotateCheckerBegin(text)
	n, _ := ts.nCommitted(index)
	if n > 0 {
		description := fmt.Sprintf("unexpected agreement at index %v", index)
		details := fmt.Sprintf("%v server(s) commit incorrectly index", n)
		tester.AnnotateCheckerFailure(description, details)
		ts.Fatalf("%v committed but no majority", n)
	}
	description := fmt.Sprintf("no unexpected agreement at index %v", index)
	tester.AnnotateCheckerSuccess(description, "OK")
}

// how many servers think a log entry is committed?
func (ts *Test) nCommitted(index int) (int, any) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	count := 0
	var cmd any = nil
	for _, rs := range ts.servers {
		if rs.applyErr != "" {
			tester.AnnotateCheckerFailure("apply error", rs.applyErr)
			ts.t.Fatal(rs.applyErr)
		}

		cmd1, ok := rs.Logs(index)

		if ok {
			if count > 0 && cmd != cmd1 {
				text := fmt.Sprintf("committed values at index %v do not match (%v != %v)",
					index, cmd, cmd1)
				tester.AnnotateCheckerFailure("unmatched committed values", text)
				ts.Fatalf(text)
			}
			count += 1
			cmd = cmd1
		}
	}
	return count, cmd
}

// do a complete agreement.
// it might choose the wrong leader initially,
// and have to re-submit after giving up.
// entirely gives up after about 10 seconds.
// indirectly checks that the servers agree on the
// same value, since nCommitted() checks this,
// as do the threads that read from applyCh.
// returns index.
// if retry==true, may submit the command multiple
// times, in case a leader fails just after Start().
// if retry==false, calls Start() only once, in order
// to simplify the early Lab 3B tests.
func (ts *Test) one(cmd any, expectedServers int, retry bool) int {
	var textRetry string
	if retry {
		textRetry = "with"
	} else {
		textRetry = "without"
	}
	textCmd := fmt.Sprintf("%v", cmd)
	textBegin := fmt.Sprintf("checking agreement of %.8s by at least %v servers %v retry",
		textCmd, expectedServers, textRetry)
	tester.AnnotateCheckerBegin(textBegin)
	t0 := time.Now()
	starts := 0
	for time.Since(t0).Seconds() < 10 && ts.checkFinished() == false {
		// try all the servers, maybe one is the leader.
		index := -1
		for range ts.servers {
			starts = (starts + 1) % len(ts.servers)
			var rf raftapi.Raft
			if ts.g.IsConnected(starts) {
				ts.servers[starts].mu.Lock()
				rf = ts.servers[starts].raft
				ts.servers[starts].mu.Unlock()
			}
			if rf != nil {
				//log.Printf("peer %d Start %v", starts, cmd)
				index1, _, ok := rf.Start(cmd)
				if ok {
					index = index1
					break
				}
			}
		}

		if index != -1 {
			// somebody claimed to be the leader and to have
			// submitted our command; wait a while for agreement.
			t1 := time.Now()
			for time.Since(t1).Seconds() < 2 {
				nd, cmd1 := ts.nCommitted(index)
				if nd > 0 && nd >= expectedServers {
					// committed
					if cmd1 == cmd {
						// and it was the command we submitted.
						description := fmt.Sprintf("agreement of %.8s reached", textCmd)
						tester.AnnotateCheckerSuccess(description, "OK")
						return index
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if retry == false {
				description := fmt.Sprintf("agreement of %.8s failed", textCmd)
				tester.AnnotateCheckerFailure(description, "failed after submitting command")
				ts.Fatalf("one(%v) failed to reach agreement", cmd)
			}
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if ts.checkFinished() == false {
		description := fmt.Sprintf("agreement of %.8s failed", textCmd)
		tester.AnnotateCheckerFailure(description, "failed after 10-second timeout")
		ts.Fatalf("one(%v) failed to reach agreement", cmd)
	}
	return -1
}

func (ts *Test) checkFinished() bool {
	z := atomic.LoadInt32(&ts.finished)
	return z != 0
}

// wait for at least n servers to commit.
// but don't wait forever.
func (ts *Test) wait(index int, n int, startTerm int) any {
	to := 10 * time.Millisecond
	for iters := 0; iters < 30; iters++ {
		nd, _ := ts.nCommitted(index)
		if nd >= n {
			break
		}
		time.Sleep(to)
		if to < time.Second {
			to *= 2
		}
		if startTerm > -1 {
			for _, rs := range ts.servers {
				if t, _ := rs.raft.GetState(); t > startTerm {
					// someone has moved on
					// can no longer guarantee that we'll "win"
					return -1
				}
			}
		}
	}
	nd, cmd := ts.nCommitted(index)
	if nd < n {
		description := fmt.Sprintf("less than %v servers commit index %v", n, index)
		details := fmt.Sprintf(
			"only %v (< %v) servers commit index %v at term %v", nd, n, index, startTerm)
		tester.AnnotateCheckerFailure(description, details)
		ts.Fatalf("only %d decided for index %d; wanted %d",
			nd, index, n)
	}
	return cmd
}
