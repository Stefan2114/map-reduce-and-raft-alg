# Lab 1: Distributed MapReduce

This project implements a distributed MapReduce system as described in the [MIT 6.5840 Lab 1 instructions](https://61824.csail.mit.edu/6.5840/labs/lab-mr.html). The goal was to build a system consisting of a central **Coordinator** and multiple **Workers** capable of parallel processing and fault recovery.

---

## 🏗️ Architecture & Component Design

The system relies on a pull-based model where stateless Workers request tasks from a stateful Coordinator via RPC.

### 1. The Coordinator (Master)
The Coordinator maintains the state of all Map and Reduce tasks. To ensure thread safety and simplify state management, I used an internal scheduling loop powered by Go channels.

**Key Data Structures:**
```go
type Task struct {
	fileName  string
	id        int
	startTime time.Time
	status    TaskStatus // Idle, InProgress, or Done
}

type Coordinator struct {
	files   []string
	NReduce int
	phase   SchedulePhase
	tasks   []Task
	heartBeatChan chan heartBeatMessage
	reportChan    chan reportMessage
}
```

#### Task Assignment Logic:
The Coordinator assigns tasks to workers when they heartbeat. If a task is `InProgress` but has exceeded the 10-second timeout, it is re-issued to the requesting worker

```go
func (c *Coordinator) assignTask(hb *heartBeatMessage) (bool, bool) {
	for i := range c.tasks {
		task := &c.tasks[i]
		switch task.status {
		case Idle:
			c.startTask(task, hb)
			return true, true
		case InProgress:
			if time.Since(task.startTime) > 10*time.Second {
				c.startTask(task, hb) // Re-assign timed-out task
				return true, true
			}
		}
	}
	return false, false
}
```

### 2. The Worker (Execution Engine)
Workers run in a continuous loop, handling tasks based on the `JobType` returned by the Coordinator.

```go
func Worker(mapFn func(string, string) []KeyValue, reduceFn func(string, []string) string) {
	for {
		resp := doHeartbeat()
		switch resp.JobType {
		case MapJob:
			doMapTask(mapFn, resp)
		case ReduceJob:
			doReduceTask(reduceFn, resp)
		case WaitJob:
			time.Sleep(time.Second)
		case CompleteJob:
			return
		}
	}
}
```

#### Fault-Tolerant File Writing:
During the Map and Reduce phases, workers write to temporary files and use `os.Rename` to atomically commit the output. This prevents other processes from reading partially written files if a worker crashes.

```go
func doMapTask(mapFn func(string, string) []KeyValue, resp HeartBeatResponse) {
	// ... Map computation ...
	for i := 0; i < resp.NReduce; i++ {
		tempName := fmt.Sprintf("mr-%d-%d-*", resp.TaskID, i)
		file, _ := os.CreateTemp(".", tempName)
		// Encode intermediate KV pairs to temp file...
		file.Close()
		finalName := fmt.Sprintf("mr-%d-%d", resp.TaskID, i)
		os.Rename(file.Name(), finalName) // Atomic commit
	}
}
```

## Key Technical Challenges

- Workers must finish all Map tasks before any Reduce tasks can begin. The Coordinator handles this transition in its `schedule()` loop by initializing the Reduce phase only when all Map tasks are marked `Done`.
- Because a worker might crash after finishing a task but before reporting it, the Coordinator and file system are designed so that re-running the same task results in the same final state.
- Maps use `ihash(key) % NReduce` to ensure that all instances of a specific key are routed to the same Reduce task.