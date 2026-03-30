package kvraft

import (
	"testing"

	"go-map-reduce-and-raft/kvtest"
	"go-map-reduce-and-raft/labrpc"
	"go-map-reduce-and-raft/tester"
)

type Test struct {
	t *testing.T
	*kvtest.Test
	part         string
	nClients     int
	nServers     int
	crash        bool
	partitions   bool
	maxRaftState int
	randomKeys   bool
}

const Gid = tester.GRP0

func MakeTest(t *testing.T, part string, nClients, nServers int, reliable bool, crash bool, partitions bool, maxRaftState int, randomKeys bool) *Test {
	ts := &Test{
		t:            t,
		part:         part,
		nClients:     nClients,
		nServers:     nServers,
		crash:        crash,
		partitions:   partitions,
		maxRaftState: maxRaftState,
		randomKeys:   randomKeys,
	}
	cfg := tester.MakeConfig(t, nServers, reliable, ts.StartKVServer)
	ts.Test = kvtest.MakeTest(t, cfg, randomKeys, ts)
	ts.Begin(ts.makeTitle())
	return ts
}

func (ts *Test) StartKVServer(servers []*labrpc.ClientEnd, gid tester.Tgid, me int, persister *tester.Persister) []tester.IService {
	return StartKVServer(servers, gid, me, persister, ts.maxRaftState)

}

func (ts *Test) MakeClerk() kvtest.IKVClerk {
	clnt := ts.Config.MakeClient()
	ck := MakeClerk(clnt, ts.Group(Gid).SrvNames())
	return &kvtest.TestClerk{ck, clnt}
}

func (ts *Test) DeleteClerk(ck kvtest.IKVClerk) {
	tck := ck.(*kvtest.TestClerk)
	ts.DeleteClient(tck.Clnt)
}

func (ts *Test) MakeClerkTo(to []int) kvtest.IKVClerk {
	ns := ts.Config.Group(Gid).SrvNamesTo(to)
	clnt := ts.Config.MakeClientTo(ns)
	ck := MakeClerk(clnt, ts.Group(Gid).SrvNames())
	return &kvtest.TestClerk{ck, clnt}
}

func (ts *Test) cleanup() {
	ts.Test.Cleanup()
}

func (ts *Test) makeTitle() string {
	title := "Test: "
	if ts.crash {
		// peers re-start, and thus persistence must work.
		title = title + "restarts, "
	}
	if ts.partitions {
		// the network may partition
		title = title + "partitions, "
	}
	if ts.maxRaftState != -1 {
		title = title + "snapshots, "
	}
	if ts.randomKeys {
		title = title + "random keys, "
	}
	if ts.nClients > 1 {
		title = title + "many clients"
	} else {
		title = title + "one client"
	}
	title = title + " (" + ts.part + ")" // 4A, 4B, 4C
	return title
}
