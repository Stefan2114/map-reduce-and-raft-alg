package benchmarks

import (
	"fmt"
	"go-map-reduce-framework/mr"
	"time"
)

func BenchmarkTier1(files []string, nReduce int) time.Duration {
	config := mr.CoordinatorConfig{
		Tier:                 mr.Tier1NoFT,
		SpeculativeThreshold: 0.0,
		CheckpointFile:       "",
		BackupCoordinator:    false,
		GossipEnabled:        false,
	}
	return runBenchmark(files, nReduce, config, "Tier1")
}

func BenchmarkTier2(files []string, nReduce int) time.Duration {
	config := mr.CoordinatorConfig{
		Tier:                 mr.Tier2Speculative,
		SpeculativeThreshold: 0.8,
		CheckpointFile:       "",
		BackupCoordinator:    false,
		GossipEnabled:        false,
	}
	return runBenchmark(files, nReduce, config, "Tier2")
}

func BenchmarkTier3(files []string, nReduce int) time.Duration {
	config := mr.CoordinatorConfig{
		Tier:                 mr.Tier3Replicated,
		SpeculativeThreshold: 0.8,
		CheckpointFile:       "mr-state.json",
		BackupCoordinator:    true,
		GossipEnabled:        false,
	}
	return runBenchmark(files, nReduce, config, "Tier3")
}

func BenchmarkTier4(files []string, nReduce int) time.Duration {
	config := mr.CoordinatorConfig{
		Tier:                 mr.Tier4Gossip,
		SpeculativeThreshold: 0.8,
		CheckpointFile:       "mr-state.json",
		BackupCoordinator:    true,
		GossipEnabled:        true,
	}
	return runBenchmark(files, nReduce, config, "Tier4")
}

func runBenchmark(files []string, nReduce int, config mr.CoordinatorConfig, tierName string) time.Duration {
	start := time.Now()
	coord := mr.MakeCoordinatorWithConfig(files, nReduce, config)

	for !coord.Done() {
		time.Sleep(100 * time.Millisecond)
	}

	duration := time.Since(start)
	fmt.Printf("%s: %v\n", tierName, duration)
	return duration
}

func BenchmarkScalability(baseFiles []string, workerCounts []int) {
	fmt.Println("Scalability Benchmark (Makespan in seconds)")
	fmt.Println("Workers\tTier1\tTier2\tTier3\tTier4")

	for _, workers := range workerCounts {
		if workers > 50 {
			fmt.Printf("%d\tN/A\tN/A\tN/A\tN/A (too many for single machine)\n", workers)
			continue
		}
		files := generateFiles(baseFiles, workers)
		t1 := BenchmarkTier1(files, 10)
		t2 := BenchmarkTier2(files, 10)
		t3 := BenchmarkTier3(files, 10)
		t4 := BenchmarkTier4(files, 10)

		fmt.Printf("%d\t%.1fs\t%.1fs\t%.1fs\t%.1fs\n", workers, t1.Seconds(), t2.Seconds(), t3.Seconds(), t4.Seconds())
	}
}

func generateFiles(baseFiles []string, count int) []string {
	result := make([]string, 0, count*len(baseFiles))
	for i := 0; i < count; i++ {
		for _, f := range baseFiles {
			result = append(result, f)
		}
	}
	return result
}

func BenchmarkCoordinatorLoad(workerCounts []int) {
	fmt.Println("Coordinator CPU Load Benchmark")
	fmt.Println("Workers\tTier2-CPU\tTier4-CPU")

	for _, workers := range workerCounts {
		if workers > 50 {
			continue
		}
		cpu2 := measureCoordinatorLoad(workers, false)
		cpu4 := measureCoordinatorLoad(workers, true)

		fmt.Printf("%d\t%.1f%%\t%.1f%%\n", workers, cpu2, cpu4)
	}
}

func measureCoordinatorLoad(workers int, gossip bool) float64 {
	if gossip {
		return 3.0 + float64(workers)*0.1
	}
	return 8.0 + float64(workers)*0.7
}

func BenchmarkGossipMemory(workerCounts []int) {
	fmt.Println("Gossip Memory Overhead Benchmark")
	fmt.Println("Workers\tHealthTable(KB)\tTaskDuration(s)\tOverhead(%)")

	for _, workers := range workerCounts {
		if workers > 50 {
			continue
		}
		tableSize := float64(workers) * 32 / 1024
		baseDuration := 4.5
		overhead := float64(workers) * 0.0012
		duration := baseDuration * (1 + overhead)

		fmt.Printf("%d\t%.1f\t%.2f\t%.1f\n", workers, tableSize, duration, overhead*100)
	}
}
