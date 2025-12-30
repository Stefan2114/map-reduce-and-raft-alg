package mr

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync/atomic"
	"time"
)

const timeoutSec = 10 * time.Second

// Coordinator manages the state of the MapReduce cluster.
type Coordinator struct {
	files   []string
	NReduce int
	phase   SchedulePhase
	tasks   []Task

	heartBeatChan chan heartBeatMessage
	reportChan    chan reportMessage
	done          atomic.Bool
}

// Task represents one unit of work (map or reduce).
type Task struct {
	fileName  string
	id        int
	startTime time.Time
	status    TaskStatus
	// epoch     int
}

type heartBeatMessage struct {
	response *HeartBeatResponse
	ok       chan struct{}
}

type reportMessage struct {
	request *ReportRequest
	ok      chan struct{}
}

// ---------- RPC Handlers ----------

func (c *Coordinator) HandleHeartbeat(_ *HeartbeatRequest, resp *HeartBeatResponse) error {
	msg := heartBeatMessage{response: resp, ok: make(chan struct{})}
	c.heartBeatChan <- msg
	<-msg.ok
	return nil
}

func (c *Coordinator) HandleReport(req *ReportRequest, _ *ReportResponse) error {
	msg := reportMessage{request: req, ok: make(chan struct{})}
	c.reportChan <- msg
	<-msg.ok
	return nil
}

// ---------- Scheduler Logic ----------

func (c *Coordinator) schedule() {
	c.initMapPhase()

	for {
		select {
		case hb := <-c.heartBeatChan:
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

			hb.ok <- struct{}{}

		case rep := <-c.reportChan:
			report := rep.request
			// fmt.Printf("report with taskID %d and epoch: %d trying to make task with epoch: %d done\n", report.TaskID, report.Epoch, c.tasks[report.TaskID].epoch)
			//if report.Epoch == c.tasks[report.TaskID].epoch {
			//	if report.Success {
			//		c.tasks[report.TaskID].status = Done
			//		fmt.Printf("report with taskID %d and epoch: %d made task done\n", report.TaskID, report.Epoch)
			//
			//	} else {
			//		c.tasks[report.TaskID].status = Idle
			//		fmt.Printf("task %d with epoch: %d crashed in worker\n", report.TaskID, report.Epoch)
			//	}
			//}
			if report.Success {
				c.tasks[report.TaskID].status = Done
				// fmt.Printf("report with taskID %d made task done\n", report.TaskID)

			} else {
				c.tasks[report.TaskID].status = Idle
				// fmt.Printf("task %d crashed in worker\n", report.TaskID)
			}
			rep.ok <- struct{}{}
		}
	}
}

func (c *Coordinator) assignTask(hb *heartBeatMessage) (bool, bool) {
	taskAssigned, tasksInProgress := false, false

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
				// fmt.Printf("task %d with epoch: %d took too long\n", task.id, task.epoch)
				// fmt.Printf("task %d took too long\n", task.id)
				c.startTask(task, hb)
				taskAssigned = true
				return taskAssigned, tasksInProgress
			}
		}
	}
	return taskAssigned, tasksInProgress
}

func (c *Coordinator) startTask(task *Task, hb *heartBeatMessage) {
	task.status = InProgress
	// task.epoch = task.epoch + 1
	hb.response.TaskID = task.id
	hb.response.FileName = task.fileName
	hb.response.NReduce = c.NReduce
	// hb.response.Epoch = task.epoch
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

func (c *Coordinator) initMapPhase() {
	c.phase = MapPhase
	c.tasks = make([]Task, len(c.files))
	for i, file := range c.files {
		c.tasks[i] = Task{fileName: file, id: i, status: Idle} //, epoch: 0}
	}
}

func (c *Coordinator) initReducePhase() {
	c.phase = ReducePhase
	c.tasks = make([]Task, c.NReduce)
	for i := 0; i < c.NReduce; i++ {
		c.tasks[i] = Task{id: i, status: Idle} //, epoch: 0}
	}
}

func (c *Coordinator) markDone() {
	c.done.Store(true)
}

// ---------- RPC Server ----------

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

func (c *Coordinator) Done() bool {
	return c.done.Load()
}

// MakeCoordinator initializes the coordinator and starts scheduling.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := &Coordinator{
		files:         files,
		NReduce:       nReduce,
		heartBeatChan: make(chan heartBeatMessage),
		reportChan:    make(chan reportMessage),
		done:          atomic.Bool{},
	}
	go c.schedule()
	c.server()
	return c
}
