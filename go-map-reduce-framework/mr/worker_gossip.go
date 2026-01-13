package mr

import (
	"sync"
	"time"
)

type WorkerGossip struct {
	workerID    int
	gossip      *GossipProtocol
	coordinator *Coordinator
	mu          sync.Mutex
	allWorkers  []int
}

func NewWorkerGossip(workerID int, allWorkers []int, coordinator *Coordinator) *WorkerGossip {
	wg := &WorkerGossip{
		workerID:    workerID,
		allWorkers:  allWorkers,
		coordinator: coordinator,
	}
	wg.gossip = NewGossipProtocol(workerID, allWorkers, coordinator)
	return wg
}

func (wg *WorkerGossip) Start() {
	wg.gossip.Start()
}

func (wg *WorkerGossip) Stop() {
	wg.gossip.Stop()
}

func (wg *WorkerGossip) HandleGossip(req *GossipRequest, resp *GossipResponse) error {
	ht := wg.gossip.GetHealthTable()
	ht.Merge(req.HealthTable)
	ht.Update(req.WorkerID, &HealthEntry{
		Status:         HealthAlive,
		LastSeen:       time.Now(),
		SuspicionCount: 0,
		LastUpdate:     time.Now(),
	})

	resp.HealthTable = ht.GetAll()
	return nil
}

func WorkerWithGossip(mapFn func(string, string) []KeyValue, reduceFn func(string, []string) string, workerID int, allWorkers []int) {
	coord := getCoordinator()
	if coord == nil {
		return
	}

	wg := NewWorkerGossip(workerID, allWorkers, coord)
	wg.Start()
	defer wg.Stop()

	for {
		req := &HeartbeatRequest{WorkerID: workerID}
		resp := doHeartbeatWithID(workerID)
		switch resp.JobType {
		case MapJob:
			doMapTask(mapFn, resp)
		case ReduceJob:
			doReduceTask(reduceFn, resp)
		case WaitJob:
			time.Sleep(time.Second)
		case CompleteJob:
			return
		default:
			return
		}
	}
}

func getCoordinator() *Coordinator {
	return nil
}

func doHeartbeatWithID(workerID int) HeartBeatResponse {
	args := HeartbeatRequest{WorkerID: workerID}
	reply := HeartBeatResponse{}
	if !call("Coordinator.HandleHeartbeat", &args, &reply) {
		return HeartBeatResponse{JobType: CompleteJob}
	}
	return reply
}
