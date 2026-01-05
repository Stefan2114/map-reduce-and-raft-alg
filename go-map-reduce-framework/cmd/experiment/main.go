package main

import (
	"flag"
	"fmt"
	"go-map-reduce-framework/analysis"
	"go-map-reduce-framework/experiments"
)

func main() {
	var tier = flag.Int("tier", 0, "Tier to run (1-4, 0=all)")
	var benchmark = flag.Bool("benchmark", false, "Run benchmarks")
	var analyze = flag.Bool("analyze", false, "Analyze results")
	flag.Parse()

	if *benchmark {
		runBenchmarks(*tier)
		return
	}

	if *analyze {
		analysis.GenerateReport()
		return
	}

	fmt.Println("MapReduce Fault Tolerance Research")
	fmt.Println("===================================")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  -tier=N       Run specific tier (1-4)")
	fmt.Println("  -benchmark    Run performance benchmarks")
	fmt.Println("  -analyze      Analyze experimental results")
	fmt.Println()
	fmt.Println("Running all experiments...")
	experiments.RunAllExperiments()
}

func runBenchmarks(tier int) {
	if tier == 0 {
		fmt.Println("Running all tier benchmarks...")
	} else {
		fmt.Printf("Running Tier %d benchmark...\n", tier)
	}
}
