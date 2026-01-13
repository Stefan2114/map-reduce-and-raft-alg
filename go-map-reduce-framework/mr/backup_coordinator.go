package mr

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type BackupCoordinator struct {
	primary        *Coordinator
	checkpointFile string
	leaseTimeout   time.Duration
	mu             sync.Mutex
	active         bool
}

func NewBackupCoordinator(primary *Coordinator, checkpointFile string) *BackupCoordinator {
	return &BackupCoordinator{
		primary:        primary,
		checkpointFile: checkpointFile,
		leaseTimeout:   5 * time.Second,
		active:         false,
	}
}

func (bc *BackupCoordinator) Monitor() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if bc.checkPrimaryFailure() {
				bc.failover()
				return
			}
		}
	}
}

func (bc *BackupCoordinator) checkPrimaryFailure() bool {
	info, err := os.Stat(bc.checkpointFile)
	if err != nil {
		return false
	}

	return time.Since(info.ModTime()) > bc.leaseTimeout
}

func (bc *BackupCoordinator) failover() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.active {
		return
	}

	state, err := bc.loadCheckpoint()
	if err != nil {
		return
	}

	bc.primary.mu.Lock()
	bc.primary.tasks = state.Tasks
	bc.primary.phase = state.Phase
	bc.primary.workerLeases = state.WorkerLeases
	bc.primary.mu.Unlock()

	bc.active = true
}

func (bc *BackupCoordinator) loadCheckpoint() (*CoordinatorState, error) {
	data, err := os.ReadFile(bc.checkpointFile)
	if err != nil {
		return nil, err
	}

	var state CoordinatorState
	err = json.Unmarshal(data, &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}
