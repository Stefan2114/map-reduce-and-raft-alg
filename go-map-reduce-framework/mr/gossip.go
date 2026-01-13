package mr

import (
	"math/rand"
	"sync"
	"time"
)

type HealthStatus int

const (
	HealthAlive HealthStatus = iota
	HealthSuspect
	HealthDead
)

type HealthEntry struct {
	Status         HealthStatus
	LastSeen       time.Time
	SuspicionCount int
	LastUpdate     time.Time
}

type HealthTable struct {
	mu    sync.RWMutex
	table map[int]*HealthEntry
}

type GossipMessage struct {
	WorkerID    int
	HealthTable map[int]*HealthEntry
	Timestamp   time.Time
}

type GossipProtocol struct {
	workerID           int
	neighbors          []int
	healthTable        *HealthTable
	gossipInterval     time.Duration
	consensusThreshold int
	suspicionTimeout   time.Duration
	mu                 sync.RWMutex
	coordinator        *Coordinator
	stopChan           chan struct{}
}

func NewHealthTable() *HealthTable {
	return &HealthTable{
		table: make(map[int]*HealthEntry),
	}
}

func (ht *HealthTable) Update(workerID int, entry *HealthEntry) {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	ht.table[workerID] = entry
}

func (ht *HealthTable) Get(workerID int) (*HealthEntry, bool) {
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	entry, ok := ht.table[workerID]
	return entry, ok
}

func (ht *HealthTable) GetAll() map[int]*HealthEntry {
	ht.mu.RLock()
	defer ht.mu.RUnlock()
	result := make(map[int]*HealthEntry)
	for k, v := range ht.table {
		result[k] = &HealthEntry{
			Status:         v.Status,
			LastSeen:       v.LastSeen,
			SuspicionCount: v.SuspicionCount,
			LastUpdate:     v.LastUpdate,
		}
	}
	return result
}

func (ht *HealthTable) Merge(other map[int]*HealthEntry) {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	now := time.Now()
	for workerID, otherEntry := range other {
		if workerID == 0 {
			continue
		}
		existing, exists := ht.table[workerID]
		if !exists || otherEntry.LastUpdate.After(existing.LastUpdate) {
			ht.table[workerID] = &HealthEntry{
				Status:         otherEntry.Status,
				LastSeen:       otherEntry.LastSeen,
				SuspicionCount: otherEntry.SuspicionCount,
				LastUpdate:     now,
			}
		} else if otherEntry.SuspicionCount > existing.SuspicionCount {
			existing.SuspicionCount = otherEntry.SuspicionCount
			if otherEntry.Status == HealthSuspect && existing.Status == HealthAlive {
				existing.Status = HealthSuspect
			}
		}
	}
}

func NewGossipProtocol(workerID int, allWorkers []int, coordinator *Coordinator) *GossipProtocol {
	gp := &GossipProtocol{
		workerID:           workerID,
		healthTable:        NewHealthTable(),
		gossipInterval:     100 * time.Millisecond,
		consensusThreshold: 3,
		suspicionTimeout:   300 * time.Millisecond,
		coordinator:        coordinator,
		stopChan:           make(chan struct{}),
	}

	for _, w := range allWorkers {
		if w != workerID {
			gp.healthTable.Update(w, &HealthEntry{
				Status:         HealthAlive,
				LastSeen:       time.Now(),
				SuspicionCount: 0,
				LastUpdate:     time.Now(),
			})
		}
	}

	gp.selectNeighbors(allWorkers)
	return gp
}

func (gp *GossipProtocol) selectNeighbors(allWorkers []int) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	k := 3
	if len(allWorkers) < k {
		k = len(allWorkers) - 1
	}

	neighbors := make([]int, 0, k)
	used := make(map[int]bool)
	used[gp.workerID] = true

	for len(neighbors) < k && len(neighbors) < len(allWorkers)-1 {
		idx := rand.Intn(len(allWorkers))
		wid := allWorkers[idx]
		if !used[wid] {
			neighbors = append(neighbors, wid)
			used[wid] = true
		}
	}

	gp.neighbors = neighbors
}

func (gp *GossipProtocol) Start() {
	go gp.gossipLoop()
	go gp.healthCheckLoop()
}

func (gp *GossipProtocol) Stop() {
	close(gp.stopChan)
}

func (gp *GossipProtocol) gossipLoop() {
	ticker := time.NewTicker(gp.gossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-gp.stopChan:
			return
		case <-ticker.C:
			gp.sendGossip()
		}
	}
}

func (gp *GossipProtocol) sendGossip() {
	gp.mu.RLock()
	neighbors := make([]int, len(gp.neighbors))
	copy(neighbors, gp.neighbors)
	gp.mu.RUnlock()

	msg := &GossipMessage{
		WorkerID:    gp.workerID,
		HealthTable: gp.healthTable.GetAll(),
		Timestamp:   time.Now(),
	}

	for _, neighborID := range neighbors {
		go gp.sendGossipToNeighbor(neighborID, msg)
	}
}

func (gp *GossipProtocol) sendGossipToNeighbor(neighborID int, msg *GossipMessage) {
	req := &GossipRequest{
		WorkerID:    msg.WorkerID,
		HealthTable: msg.HealthTable,
		Timestamp:   msg.Timestamp,
	}
	resp := &GossipResponse{}

	if callWorker(neighborID, "Worker.HandleGossip", req, resp) {
		gp.healthTable.Merge(resp.HealthTable)
		gp.healthTable.Update(neighborID, &HealthEntry{
			Status:         HealthAlive,
			LastSeen:       time.Now(),
			SuspicionCount: 0,
			LastUpdate:     time.Now(),
		})
	} else {
		entry, exists := gp.healthTable.Get(neighborID)
		if !exists {
			return
		}

		if entry.Status == HealthAlive {
			entry.Status = HealthSuspect
			entry.SuspicionCount = 1
		} else {
			entry.SuspicionCount++
		}
		entry.LastUpdate = time.Now()
		gp.healthTable.Update(neighborID, entry)
	}
}

func (gp *GossipProtocol) healthCheckLoop() {
	ticker := time.NewTicker(gp.suspicionTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-gp.stopChan:
			return
		case <-ticker.C:
			gp.checkSuspicions()
		}
	}
}

func (gp *GossipProtocol) checkSuspicions() {
	allEntries := gp.healthTable.GetAll()
	suspicionCounts := make(map[int]int)

	for workerID, entry := range allEntries {
		if entry.Status == HealthSuspect || entry.Status == HealthDead {
			suspicionCounts[workerID] = entry.SuspicionCount
		}
	}

	for workerID, count := range suspicionCounts {
		if count >= gp.consensusThreshold {
			entry, _ := gp.healthTable.Get(workerID)
			if entry != nil && entry.Status != HealthDead {
				entry.Status = HealthDead
				entry.LastUpdate = time.Now()
				gp.healthTable.Update(workerID, entry)

				if gp.coordinator != nil {
					gp.coordinator.ReportWorkerFailure(workerID)
				}
			}
		}
	}
}

func callWorker(workerID int, method string, args interface{}, reply interface{}) bool {
	return false
}

func (gp *GossipProtocol) GetHealthTable() *HealthTable {
	return gp.healthTable
}
