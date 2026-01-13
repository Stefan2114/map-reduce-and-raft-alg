package mr

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type FaultToleranceTier int

const (
	Tier1NoFT FaultToleranceTier = iota
	Tier2Speculative
	Tier3Replicated
	Tier4Gossip
)

type CoordinatorConfig struct {
	Tier                 FaultToleranceTier
	SpeculativeThreshold float64
	CheckpointFile       string
	BackupCoordinator    bool
	GossipEnabled        bool
}

type Coordinator struct {
	files   []string
	NReduce int
	phase   SchedulePhase
	tasks   []Task
	config  CoordinatorConfig

	heartBeatChan chan heartBeatMessage
	reportChan    chan reportMessage
	failureChan   chan int
	done          atomic.Bool

	mu                sync.Mutex
	workerLeases      map[int]time.Time
	lastCheckpoint    time.Time
	backupCoordinator *BackupCoordinator
	gossipWorkers     map[int]bool
}

type Task struct {
	fileName    string
	id          int
	startTime   time.Time
	status      TaskStatus
	workerID    int
	backupTasks []int
}

type heartBeatMessage struct {
	response *HeartBeatResponse
	ok       chan struct{}
	workerID int
}

type reportMessage struct {
	request  *ReportRequest
	ok       chan struct{}
	workerID int
}

func (c *Coordinator) HandleHeartbeat(req *HeartbeatRequest, resp *HeartBeatResponse) error {
	msg := heartBeatMessage{response: resp, ok: make(chan struct{}), workerID: req.WorkerID}
	c.heartBeatChan <- msg
	<-msg.ok
	return nil
}

func (c *Coordinator) server() {
	_ = rpc.RegisterName("Coordinator", c)
	rpc.HandleHTTP()

	sock := coordinatorSock()
	_ = os.Remove(sock)

	l, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatal("listen error:", err)
	}
	go http.Serve(l, nil)
}

func (c *Coordinator) HandleReport(req *ReportRequest, _ *ReportResponse) error {
	msg := reportMessage{request: req, ok: make(chan struct{}), workerID: req.WorkerID}
	c.reportChan <- msg
	<-msg.ok
	return nil
}

func (c *Coordinator) HandleFailureReport(req *FailureReportRequest, _ *FailureReportResponse) error {
	c.failureChan <- req.WorkerID
	return nil
}

func (c *Coordinator) ReportWorkerFailure(workerID int) {
	select {
	case c.failureChan <- workerID:
	default:
	}
}

func (c *Coordinator) schedule() {
	c.initMapPhase()

	for {
		select {
		case hb := <-c.heartBeatChan:
			c.updateWorkerLease(hb.workerID)
			taskAssigned, tasksInProgress := c.assignTask(&hb)

			switch {
			case tasksInProgress && !taskAssigned:
				hb.response.JobType = WaitJob
			case !tasksInProgress && c.phase == MapPhase:
				c.initReducePhase()
				c.assignTask(&hb)
			case !tasksInProgress && c.phase == ReducePhase:
				c.markDone()
				hb.response.JobType = CompleteJob
			}

			if c.config.Tier >= Tier2Speculative {
				c.checkSpeculativeExecution()
			}

			hb.ok <- struct{}{}

		case rep := <-c.reportChan:
			report := rep.request
			c.mu.Lock()
			if report.Success {
				c.tasks[report.TaskID].status = Done
				c.tasks[report.TaskID].backupTasks = nil
			} else {
				c.tasks[report.TaskID].status = Idle
				c.tasks[report.TaskID].workerID = 0
			}
			c.mu.Unlock()

			if c.config.Tier >= Tier3Replicated {
				c.checkpointState()
			}

			rep.ok <- struct{}{}

		case workerID := <-c.failureChan:
			c.handleWorkerFailure(workerID)
		}
	}
}

func (c *Coordinator) assignTask(hb *heartBeatMessage) (bool, bool) {
	taskAssigned, tasksInProgress := false, false
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.tasks {
		task := &c.tasks[i]

		switch task.status {
		case Idle:
			c.startTask(task, hb)
			taskAssigned, tasksInProgress = true, true
			return taskAssigned, tasksInProgress

		case InProgress:
			tasksInProgress = true
			if time.Since(task.startTime) > timeoutSec {
				if c.config.Tier >= Tier2Speculative && len(task.backupTasks) == 0 {
					c.launchBackupTask(task)
				} else {
					c.startTask(task, hb)
					taskAssigned = true
				}
				return taskAssigned, tasksInProgress
			}
		}
	}
	return taskAssigned, tasksInProgress
}

func (c *Coordinator) startTask(task *Task, hb *heartBeatMessage) {
	task.status = InProgress
	task.workerID = hb.workerID
	hb.response.TaskID = task.id
	hb.response.FileName = task.fileName
	hb.response.NReduce = c.NReduce
	switch c.phase {
	case MapPhase:
		hb.response.JobType = MapJob
	case ReducePhase:
		hb.response.JobType = ReduceJob
	default:
		panic(fmt.Sprintf("invalid phase %v", c.phase))
	}
	task.startTime = time.Now()
}

func (c *Coordinator) checkSpeculativeExecution() {
	c.mu.Lock()
	defer c.mu.Unlock()

	completed := 0
	total := len(c.tasks)
	for _, task := range c.tasks {
		if task.status == Done {
			completed++
		}
	}

	threshold := float64(completed) / float64(total)
	if threshold >= c.config.SpeculativeThreshold {
		for i := range c.tasks {
			task := &c.tasks[i]
			if task.status == InProgress && len(task.backupTasks) == 0 {
				c.launchBackupTask(task)
			}
		}
	}
}

func (c *Coordinator) launchBackupTask(task *Task) {
	task.backupTasks = append(task.backupTasks, 0)
}

func (c *Coordinator) handleWorkerFailure(workerID int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.tasks {
		task := &c.tasks[i]
		if task.workerID == workerID && task.status == InProgress {
			task.status = Idle
			task.workerID = 0
			task.backupTasks = nil
		}
	}

	delete(c.workerLeases, workerID)
	if c.config.Tier >= Tier3Replicated {
		c.checkpointState()
	}
}

func (c *Coordinator) updateWorkerLease(workerID int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workerLeases[workerID] = time.Now()
}

func (c *Coordinator) checkpointState() {
	if time.Since(c.lastCheckpoint) < 100*time.Millisecond {
		return
	}

	state := CoordinatorState{
		Tasks:        c.tasks,
		Phase:        c.phase,
		WorkerLeases: c.workerLeases,
		Timestamp:    time.Now(),
	}

	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	tmpFile := c.config.CheckpointFile + ".tmp"
	err = os.WriteFile(tmpFile, data, 0644)
	if err != nil {
		return
	}

	err = os.Rename(tmpFile, c.config.CheckpointFile)
	if err != nil {
		return
	}

	c.lastCheckpoint = time.Now()
}

type CoordinatorState struct {
	Tasks        []Task
	Phase        SchedulePhase
	WorkerLeases map[int]time.Time
	Timestamp    time.Time
}

func (c *Coordinator) initMapPhase() {
	c.phase = MapPhase
	c.tasks = make([]Task, len(c.files))
	for i, file := range c.files {
		c.tasks[i] = Task{fileName: file, id: i, status: Idle}
	}
}

func (c *Coordinator) initReducePhase() {
	c.phase = ReducePhase
	c.tasks = make([]Task, c.NReduce)
	for i := 0; i < c.NReduce; i++ {
		c.tasks[i] = Task{id: i, status: Idle}
	}
}

func (c *Coordinator) markDone() {
	c.done.Store(true)
}

func (c *Coordinator) Done() bool {
	return c.done.Load()
}

func MakeCoordinator(files []string, nReduce int) *Coordinator {
	return MakeCoordinatorWithConfig(files, nReduce, CoordinatorConfig{
		Tier:                 Tier1NoFT,
		SpeculativeThreshold: 0.8,
		CheckpointFile:       "mr-state.json",
		BackupCoordinator:    false,
		GossipEnabled:        false,
	})
}

func MakeCoordinatorWithConfig(files []string, nReduce int, config CoordinatorConfig) *Coordinator {
	c := &Coordinator{
		files:         files,
		NReduce:       nReduce,
		config:        config,
		heartBeatChan: make(chan heartBeatMessage),
		reportChan:    make(chan reportMessage),
		failureChan:   make(chan int, 100),
		done:          atomic.Bool{},
		workerLeases:  make(map[int]time.Time),
		gossipWorkers: make(map[int]bool),
	}

	if config.Tier >= Tier3Replicated && config.BackupCoordinator {
		c.backupCoordinator = NewBackupCoordinator(c, config.CheckpointFile)
		go c.backupCoordinator.Monitor()
	}

	go c.schedule()
	c.server()
	return c
}
