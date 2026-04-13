# Graph Report: e2e-code

Generated: 2026-04-13 13:02:46

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 139 |
| Edges | 189 |
| Communities | 23 |
| Modularity (Q) | 0.5791 |
| God Nodes | 20 |
| Cross-Community Edges | 20 |

**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.

## God Nodes (High-Degree Hubs)

These symbols have an unusually high number of connections, making them central to the codebase.

| Rank | Symbol | Kind | In° | Out° | Total° |
|------|--------|------|-----|------|--------|
| 1 | `queue.ts` | file | 0 | 39 | 39 |
| 2 | `rate_limiter.py` | file | 0 | 27 | 27 |
| 3 | `parser.go` | file | 0 | 17 | 17 |
| 4 | `code.PersistentQueue_part3` | class | 1 | 10 | 11 |
| 5 | `code.LeakyBucket` | class | 1 | 9 | 10 |
| 6 | `code.PersistentQueue_part2` | class | 1 | 8 | 9 |
| 7 | `code.parseGo` | function | 2 | 7 | 9 |
| 8 | `code.parseTypeScript` | function | 2 | 7 | 9 |
| 9 | `code._InMemoryStore` | class | 2 | 6 | 8 |
| 10 | `code.extractGoFuncName` | function | 2 | 5 | 7 |
| 11 | `code.DistributedRateLimiter` | class | 1 | 6 | 7 |
| 12 | `code.generateId` | function | 2 | 5 | 7 |
| 13 | `code.parsePython` | function | 2 | 5 | 7 |
| 14 | `strings.TrimSpace` | function | 6 | 0 | 6 |
| 15 | `code.PersistentQueue_part4` | class | 1 | 5 | 6 |
| 16 | `code.TokenBucket_part2` | class | 1 | 4 | 5 |
| 17 | `code.append` | function | 5 | 0 | 5 |
| 18 | `code.TokenBucket_part1` | class | 1 | 4 | 5 |
| 19 | `strings.HasPrefix` | function | 5 | 0 | 5 |
| 20 | `code.PersistentQueue_part1` | class | 1 | 4 | 5 |

> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.

## Communities

Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.

### Community 0: queue (34 nodes, cohesion=0.009)

- `code.CircularBuffer::CircularBuffer`
- `code.CircularBuffer::CircularBuffer::constructor`
- `code.CircularBuffer::CircularBuffer::isEmpty`
- `code.CircularBuffer::CircularBuffer::isFull`
- `code.CircularBuffer::CircularBuffer::pop`
- `code.CircularBuffer::CircularBuffer::push`
- `code.CircularBuffer::CircularBuffer::size`
- `code.DEFAULT_CONFIG`
- `code.PersistentQueue::PersistentQueue::ack`
- `code.PersistentQueue::PersistentQueue::constructor`
- `code.PersistentQueue::PersistentQueue::enqueue`
- `code.PersistentQueue::PersistentQueue::inFlight`
- `code.PersistentQueue::PersistentQueue::nack`
- `code.PersistentQueue::PersistentQueue::queueName`
- `code.PersistentQueue::PersistentQueue::reEnqueueExpiredMessages`
- `code.PersistentQueue::PersistentQueue::receive`
- `code.PersistentQueue::PersistentQueue::size`
- `code.PersistentQueue::PersistentQueue::startPolling`
- `code.PersistentQueue::PersistentQueue::stopPolling`
- `code.PersistentQueue::PersistentQueue_part1`
- ... and 14 more

### Community 1: rate_limiter (21 nodes, cohesion=0.014)

- `code.DistributedRateLimiter::__init__`
- `code.DistributedRateLimiter::allow`
- `code.LeakyBucket::__init__`
- `code.LeakyBucket::_leak`
- `code.LeakyBucket::enqueue`
- `code.LeakyBucket::queue_depth`
- `code.SlidingWindowCounter::__init__`
- `code.SlidingWindowCounter::_advance_window_if_needed`
- `code.SlidingWindowCounter::_estimate_count`
- `code.SlidingWindowCounter::allow`
- `code.TokenBucket::TokenBucket::__post_init__`
- `code.TokenBucket::TokenBucket::_refill`
- `code.TokenBucket::TokenBucket::acquire`
- `code.TokenBucket::TokenBucket::wait_and_acquire`
- `code.TokenBucket::TokenBucket_part1`
- `code.TokenBucket::TokenBucket_part2`
- `code._InMemoryStore::__init__`
- `code._InMemoryStore::_evict_expired`
- `code._InMemoryStore::expire`
- `code._InMemoryStore::incr`
- ... and 1 more

### Community 2: code (6 nodes, cohesion=0.167)

- `code.LeakyBucket`
- `code._leak`
- `code.deque`
- `code.len`
- `code.popleft`
- `code.range`

### Community 3: parser (6 nodes, cohesion=0.050)

- `code.LangGo`
- `code.Language`
- `code.New`
- `code.Parser`
- `code.Symbol`
- `parser.go`

### Community 4: code (6 nodes, cohesion=0.167)

- `code.PersistentQueue_part2`
- `code.add`
- `code.delete`
- `code.entries`
- `code.has`
- `code.push`

### Community 5: code (6 nodes, cohesion=0.167)

- `code.from`
- `code.generateId`
- `code.getRandomValues`
- `code.join`
- `code.padStart`
- `code.toString`

### Community 6: code (6 nodes, cohesion=0.167)

- `code.DistributedRateLimiter`
- `code.expire`
- `code.incr`
- `code.int`
- `code.max`
- `code.time`

### Community 7: code (6 nodes, cohesion=0.167)

- `code.PersistentQueue_part3`
- `code.pow`
- `code.random`
- `code.reEnqueueExpiredMessages`
- `code.receive`
- `code.setInterval`

### Community 8: strings (5 nodes, cohesion=0.250)

- `code.extractTSFuncName`
- `code.parseTypeScript`
- `strings.Contains`
- `strings.HasPrefix`
- `strings.IndexAny`

### Community 9: parser (5 nodes, cohesion=0.300)

- `code.extractGoFuncName`
- `code.extractGoTypeName`
- `code.parseGo`
- `strings.Index`
- `strings.TrimPrefix`

### Community 10: parser (5 nodes, cohesion=0.250)

- `code.extractPyName`
- `code.extractTSClassName`
- `code.parsePython`
- `strings.Split`
- `strings.TrimSpace`

### Community 11: code (4 nodes, cohesion=0.250)

- `code.PersistentQueue_part4`
- `code.clearInterval`
- `code.emit`
- `code.get`

### Community 12: code (4 nodes, cohesion=0.250)

- `code._InMemoryStore`
- `code._evict_expired`
- `code.items`
- `code.pop`

### Community 13: code (3 nodes, cohesion=0.333)

- `code.Lock`
- `code.SlidingWindowCounter_part1`
- `code.monotonic`

### Community 14: code (3 nodes, cohesion=0.333)

- `code.TokenBucket_part2`
- `code._refill`
- `code.sleep`

### Community 15: code (3 nodes, cohesion=0.333)

- `code.PersistentQueue_part1`
- `code.now`
- `code.set`

### Community 16: code (3 nodes, cohesion=0.333)

- `code.TokenBucket_part1`
- `code.field`
- `code.min`

### Community 17: code (3 nodes, cohesion=0.333)

- `code.SlidingWindowCounter_part2`
- `code._advance_window_if_needed`
- `code._estimate_count`

### Community 18: strings (2 nodes, cohesion=0.500)

- `code.CyclomaticComplexity`
- `strings.Count`

### Community 19: parser (2 nodes, cohesion=0.500)

- `code.FilterByKind`
- `code.append`

### Community 20: parser (2 nodes, cohesion=0.500)

- `code.CountSymbols`
- `code.make`

### Community 21: code (2 nodes, cohesion=0.500)

- `code.CircularBuffer`
- `code.fill`

### Community 22: parser (2 nodes, cohesion=0.500)

- `code.Parser.Parse`
- `fmt.Errorf`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `code.extractTSClassName` | `strings.Index` | 10 → 9 | 1.50 |
| `code.PersistentQueue_part4` | `code.now` | 11 → 15 | 1.20 |
| `code.parseTypeScript` | `strings.TrimSpace` | 8 → 10 | 1.50 |
| `code.parseTypeScript` | `code.append` | 8 → 19 | 1.20 |
| `code.parsePython` | `code.append` | 10 → 19 | 1.20 |
| `code.PersistentQueue_part1` | `code.generateId` | 15 → 5 | 1.20 |
| `code.extractGoFuncName` | `strings.IndexAny` | 9 → 8 | 1.50 |
| `code.extractGoFuncName` | `strings.TrimSpace` | 9 → 10 | 1.50 |
| `code.monotonic` | `code.TokenBucket_part2` | 13 → 14 | 1.00 |
| `code.int` | `code.LeakyBucket` | 6 → 2 | 1.20 |
| `code.parseGo` | `code.append` | 9 → 19 | 1.20 |
| `code.parseGo` | `strings.TrimSpace` | 9 → 10 | 1.50 |
| `code.Parser.Parse` | `code.parseTypeScript` | 22 → 8 | 1.80 |
| `code.Parser.Parse` | `code.parseGo` | 22 → 9 | 1.80 |
| `code.Parser.Parse` | `code.parsePython` | 22 → 10 | 1.80 |
| `code.LeakyBucket` | `code.append` | 2 → 19 | 1.20 |
| `code.LeakyBucket` | `code.monotonic` | 2 → 13 | 1.20 |
| `code.LeakyBucket` | `code.Lock` | 2 → 13 | 1.20 |
| `strings.Index` | `code.extractPyName` | 9 → 10 | 1.50 |
| `code.DistributedRateLimiter` | `code._InMemoryStore` | 6 → 12 | 1.20 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `queue.ts` (degree 39) were refactored?
2. Is `rate_limiter.py` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'queue' and 'rate_limiter' share cross-module edges?
4. What is the relationship between `code.extractTSClassName` and `strings.Index` (surprising cross-community edge)?

