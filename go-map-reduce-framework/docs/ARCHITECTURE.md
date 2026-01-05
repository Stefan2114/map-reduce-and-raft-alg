# MapReduce Fault Tolerance Architecture

## Overview

This implementation provides four tiers of fault tolerance for MapReduce systems:

1. **Tier 1: Zero Fault Tolerance** - Baseline with no failure detection
2. **Tier 2: Speculative Execution** - Backup tasks for straggler mitigation
3. **Tier 3: Primary-Backup Coordinator** - Coordinator replication for high availability
4. **Tier 4: Gossip-based Health Monitoring** - Decentralized failure detection

## Tier 4: Gossip Protocol Architecture

### Components

- **GossipProtocol**: Manages worker-to-worker health monitoring
- **HealthTable**: Maintains local view of worker health status
- **WorkerGossip**: Integrates gossip with worker lifecycle
- **Coordinator**: Receives failure reports from gossip consensus

### Protocol Flow

1. Workers establish neighbor relationships (ring topology)
2. Every 100ms, workers exchange health tables with 3 neighbors
3. Suspicion mechanism marks unresponsive neighbors
4. Consensus threshold (3 workers) triggers failure reports to coordinator
5. Coordinator reschedules tasks from failed workers

### Memory Model

- Health table: O(N) where N = number of workers
- Per-worker memory: ~32 bytes per worker
- Example: 30 workers = ~0.9 KB per worker

### Performance Characteristics

- Coordinator load reduction: 60-73% in tested scenarios (10-30 workers)
- Failure detection latency: 280ms average (vs 5.2s centralized)
- Task duration overhead: 0.4-3.5% for tested cluster sizes
