package mr

import (
	"os"
	"strconv"
)

type HeartbeatRequest struct{}

type HeartBeatResponse struct {
	JobType  JobType
	FileName string
	TaskID   int
	NReduce  int
	Epoch    int
}

type ReportRequest struct {
	TaskID int
	// JobType JobType
	// Epoch   int
	Success bool
}

type ReportResponse struct{}

type JobType int

const (
	MapJob      JobType = 0
	ReduceJob   JobType = 1
	WaitJob     JobType = 2
	CompleteJob JobType = 3
)

type TaskStatus int

const (
	Idle       TaskStatus = 0
	InProgress TaskStatus = 1
	Done       TaskStatus = 2
)

type SchedulePhase int

const (
	MapPhase    SchedulePhase = 0
	ReducePhase SchedulePhase = 1
)

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/5840-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
