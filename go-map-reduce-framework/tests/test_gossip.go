package tests

import (
	"go-map-reduce-framework/mr"
	"testing"
	"time"
)

func TestGossipHealthTable(t *testing.T) {
	ht := mr.NewHealthTable()
	
	entry := &mr.HealthEntry{
		Status:         mr.HealthAlive,
		LastSeen:       time.Now(),
		SuspicionCount: 0,
		LastUpdate:     time.Now(),
	}
	
	ht.Update(1, entry)
	
	retrieved, exists := ht.Get(1)
	if !exists {
		t.Fatal("Entry not found")
	}
	
	if retrieved.Status != mr.HealthAlive {
		t.Fatal("Status mismatch")
	}
}

func TestGossipMerge(t *testing.T) {
	ht1 := mr.NewHealthTable()
	ht2 := make(map[int]*mr.HealthEntry)
	
	entry1 := &mr.HealthEntry{
		Status:         mr.HealthAlive,
		LastSeen:       time.Now(),
		SuspicionCount: 0,
		LastUpdate:     time.Now(),
	}
	
	entry2 := &mr.HealthEntry{
		Status:         mr.HealthSuspect,
		LastSeen:       time.Now().Add(-time.Minute),
		SuspicionCount: 2,
		LastUpdate:     time.Now(),
	}
	
	ht1.Update(1, entry1)
	ht2[1] = entry2
	
	ht1.Merge(ht2)
	
	retrieved, _ := ht1.Get(1)
	if retrieved.Status != mr.HealthSuspect {
		t.Fatal("Merge failed")
	}
}

func TestSpeculativeExecution(t *testing.T) {
	files := []string{"test1.txt", "test2.txt", "test3.txt", "test4.txt", "test5.txt"}
	config := mr.CoordinatorConfig{
		Tier:               mr.Tier2Speculative,
		SpeculativeThreshold: 0.8,
		CheckpointFile:     "",
		BackupCoordinator:  false,
		GossipEnabled:      false,
	}
	
	coord := mr.MakeCoordinatorWithConfig(files, 10, config)
	if coord == nil {
		t.Fatal("Coordinator creation failed")
	}
}

func TestCoordinatorReplication(t *testing.T) {
	files := []string{"test1.txt", "test2.txt"}
	config := mr.CoordinatorConfig{
		Tier:               mr.Tier3Replicated,
		SpeculativeThreshold: 0.8,
		CheckpointFile:     "test-state.json",
		BackupCoordinator:  true,
		GossipEnabled:      false,
	}
	
	coord := mr.MakeCoordinatorWithConfig(files, 10, config)
	if coord == nil {
		t.Fatal("Coordinator creation failed")
	}
	
	time.Sleep(100 * time.Millisecond)
}

func TestGossipProtocol(t *testing.T) {
	allWorkers := []int{1, 2, 3, 4, 5}
	gp := mr.NewGossipProtocol(1, allWorkers, nil)
	
	if gp == nil {
		t.Fatal("Gossip protocol creation failed")
	}
	
	gp.Start()
	time.Sleep(200 * time.Millisecond)
	gp.Stop()
}
