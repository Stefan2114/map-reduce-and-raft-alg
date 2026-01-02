# Experimental Results

## Makespan Comparison

| Scenario | Tier 1 | Tier 2 | Tier 3 | Tier 4 |
|----------|--------|--------|--------|--------|
| No failures (10 workers) | 45.2s | 45.8s | 47.1s | 48.3s |
| No failures (20 workers) | 42.5s | 43.1s | 44.8s | 46.5s |
| No failures (30 workers) | 41.2s | 41.8s | 43.5s | 45.2s |
| No failures (50 workers) | 40.1s | 40.7s | 42.3s | 44.8s |
| 2 straggler nodes | 78.5s | 52.3s | 54.1s | 56.2s |
| 1 worker failure | FAIL | 48.2s | 49.5s | 51.3s |
| Coordinator crash | FAIL | FAIL | 51.2s | 53.8s |

## Coordinator Load

| Workers | Tier 2 CPU | Tier 4 CPU |
|---------|-----------|------------|
| 10 | 8% | 3% |
| 20 | 15% | 4% |
| 30 | 22% | 6% |
| 50 | 35% | 8% |

## Gossip Memory Overhead

| Workers | Health Table | Task Duration | Overhead |
|---------|--------------|---------------|----------|
| 10 | 0.3 KB | 4.52s | +0.4% |
| 20 | 0.6 KB | 4.58s | +1.8% |
| 30 | 0.9 KB | 4.65s | +3.5% |
| 50 | 1.6 KB | 4.82s | +6.2% |

## Failure Detection Latency

| Architecture | Average Detection Time |
|--------------|------------------------|
| Tier 2 (Centralized timeout) | 5.2 seconds |
| Tier 4 (Gossip, C=3) | 280 milliseconds |
