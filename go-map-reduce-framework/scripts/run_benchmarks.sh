#!/bin/bash

echo "Running MapReduce Fault Tolerance Benchmarks"
echo "=============================================="

cd "$(dirname "$0")/.."

echo ""
echo "1. Building benchmarks..."
go build -o bin/benchmark ./benchmarks

echo ""
echo "2. Running Tier 1 (Baseline) benchmark..."
./bin/benchmark tier1

echo ""
echo "3. Running Tier 2 (Speculative) benchmark..."
./bin/benchmark tier2

echo ""
echo "4. Running Tier 3 (Replicated) benchmark..."
./bin/benchmark tier3

echo ""
echo "5. Running Tier 4 (Gossip) benchmark..."
./bin/benchmark tier4

echo ""
echo "6. Running scalability test..."
./bin/benchmark scalability

echo ""
echo "Benchmarks completed!"
