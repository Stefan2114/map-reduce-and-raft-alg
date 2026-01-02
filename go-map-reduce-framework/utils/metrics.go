package utils

import (
	"sync"
	"time"
)

type Metrics struct {
	mu                sync.RWMutex
	coordinatorCPU     float64
	networkPackets     int
	taskDuration       time.Duration
	healthTableSize    int
	gossipMessages     int
	failureDetections  int
	checkpointLatency  time.Duration
}

var globalMetrics = &Metrics{}

func RecordCoordinatorCPU(cpu float64) {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.coordinatorCPU = cpu
}

func RecordNetworkPackets(packets int) {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.networkPackets = packets
}

func RecordTaskDuration(duration time.Duration) {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.taskDuration = duration
}

func RecordHealthTableSize(size int) {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.healthTableSize = size
}

func RecordGossipMessage() {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.gossipMessages++
}

func RecordFailureDetection() {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.failureDetections++
}

func RecordCheckpointLatency(latency time.Duration) {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics.checkpointLatency = latency
}

func GetMetrics() Metrics {
	globalMetrics.mu.RLock()
	defer globalMetrics.mu.RUnlock()
	return *globalMetrics
}

func ResetMetrics() {
	globalMetrics.mu.Lock()
	defer globalMetrics.mu.Unlock()
	globalMetrics = &Metrics{}
}
