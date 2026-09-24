# Graph Report: e2e-code

Generated: 2026-09-24 11:21:02

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 128 |
| Edges | 164 |
| Communities | 15 |
| Modularity (Q) | 0.7657 |
| God Nodes | 19 |
| Cross-Community Edges | 20 |

**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.

## God Nodes (High-Degree Hubs)

These symbols have an unusually high number of connections, making them central to the codebase.

| Rank | Symbol | Kind | In° | Out° | Total° |
|------|--------|------|-----|------|--------|
| 1 | `queue.ts` | file | 0 | 31 | 31 |
| 2 | `rate_limiter.py` | file | 0 | 24 | 24 |
| 3 | `parser.go` | file | 0 | 17 | 17 |
| 4 | `code.PersistentQueue` | class | 1 | 16 | 17 |
| 5 | `code.LeakyBucket` | class | 1 | 9 | 10 |
| 6 | `code.parseTypeScript` | function | 2 | 7 | 9 |
| 7 | `code.parseGo` | function | 2 | 7 | 9 |
| 8 | `code._InMemoryStore` | class | 2 | 6 | 8 |
| 9 | `code.generateId` | function | 2 | 5 | 7 |
| 10 | `code.DistributedRateLimiter` | class | 1 | 6 | 7 |
| 11 | `code.parsePython` | function | 2 | 5 | 7 |
| 12 | `code.extractGoFuncName` | function | 2 | 5 | 7 |
| 13 | `strings.TrimSpace` | function | 6 | 0 | 6 |
| 14 | `code.TokenBucket` | class | 1 | 5 | 6 |
| 15 | `strings.HasPrefix` | function | 5 | 0 | 5 |
| 16 | `code.append` | function | 5 | 0 | 5 |
| 17 | `code.extractTSFuncName` | function | 2 | 3 | 5 |
| 18 | `code.SlidingWindowCounter` | class | 1 | 4 | 5 |
| 19 | `code.Parser.Parse` | method | 1 | 4 | 5 |

> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.

## Communities

Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.

### Community 0: queue (29 nodes, cohesion=0.010)

- `code.CircularBuffer::CircularBuffer`
- `code.CircularBuffer::CircularBuffer::constructor`
- `code.CircularBuffer::CircularBuffer::isEmpty`
- `code.CircularBuffer::CircularBuffer::isFull`
- `code.CircularBuffer::CircularBuffer::pop`
- `code.CircularBuffer::CircularBuffer::push`
- `code.CircularBuffer::CircularBuffer::size`
- `code.DEFAULT_CONFIG`
- `code.PersistentQueue::PersistentQueue`
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
- ... and 9 more

### Community 1: rate_limiter (20 nodes, cohesion=0.015)

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
- `code.TokenBucket::TokenBucket`
- `code.TokenBucket::TokenBucket::__post_init__`
- `code.TokenBucket::TokenBucket::_refill`
- `code.TokenBucket::TokenBucket::acquire`
- `code.TokenBucket::TokenBucket::wait_and_acquire`
- `code._InMemoryStore::__init__`
- `code._InMemoryStore::_evict_expired`
- `code._InMemoryStore::expire`
- `code._InMemoryStore::incr`
- `rate_limiter.py`

### Community 2: parser (17 nodes, cohesion=0.121)

- `code.FilterByKind`
- `code.Parser.Parse`
- `code.append`
- `code.extractGoFuncName`
- `code.extractPyName`
- `code.extractTSClassName`
- `code.extractTSFuncName`
- `code.parseGo`
- `code.parsePython`
- `code.parseTypeScript`
- `fmt.Errorf`
- `strings.Contains`
- `strings.HasPrefix`
- `strings.Index`
- `strings.IndexAny`
- `strings.Split`
- `strings.TrimSpace`

### Community 3: code (15 nodes, cohesion=0.067)

- `code.PersistentQueue`
- `code.add`
- `code.clearInterval`
- `code.delete`
- `code.emit`
- `code.entries`
- `code.has`
- `code.now`
- `code.pow`
- `code.push`
- `code.random`
- `code.reEnqueueExpiredMessages`
- `code.receive`
- `code.set`
- `code.setInterval`

### Community 4: code (6 nodes, cohesion=0.167)

- `code.from`
- `code.generateId`
- `code.getRandomValues`
- `code.join`
- `code.padStart`
- `code.toString`

### Community 5: code (6 nodes, cohesion=0.167)

- `code.LeakyBucket`
- `code._leak`
- `code.deque`
- `code.len`
- `code.popleft`
- `code.range`

### Community 6: parser (6 nodes, cohesion=0.050)

- `code.LangGo`
- `code.Language`
- `code.New`
- `code.Parser`
- `code.Symbol`
- `parser.go`

### Community 7: code (6 nodes, cohesion=0.167)

- `code.DistributedRateLimiter`
- `code.expire`
- `code.incr`
- `code.int`
- `code.max`
- `code.time`

### Community 8: code (5 nodes, cohesion=0.200)

- `code._InMemoryStore`
- `code._evict_expired`
- `code.get`
- `code.items`
- `code.pop`

### Community 9: code (5 nodes, cohesion=0.200)

- `code.Lock`
- `code.SlidingWindowCounter`
- `code._advance_window_if_needed`
- `code._estimate_count`
- `code.monotonic`

### Community 10: code (5 nodes, cohesion=0.200)

- `code.TokenBucket`
- `code._refill`
- `code.field`
- `code.min`
- `code.sleep`

### Community 11: queue (2 nodes, cohesion=0.500)

- `code.CircularBuffer`
- `code.fill`

### Community 12: parser (2 nodes, cohesion=0.500)

- `code.CyclomaticComplexity`
- `strings.Count`

### Community 13: parser (2 nodes, cohesion=0.500)

- `code.extractGoTypeName`
- `strings.TrimPrefix`

### Community 14: parser (2 nodes, cohesion=0.500)

- `code.CountSymbols`
- `code.make`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `code.int` | `code.LeakyBucket` | 7 → 5 | 1.00 |
| `code.monotonic` | `code.LeakyBucket` | 9 → 5 | 1.20 |
| `code._InMemoryStore` | `code.monotonic` | 8 → 9 | 1.00 |
| `code.TokenBucket` | `code.monotonic` | 10 → 9 | 1.00 |
| `code.get` | `code.PersistentQueue` | 8 → 3 | 1.20 |
| `code.generateId` | `code.PersistentQueue` | 4 → 3 | 1.00 |
| `code.append` | `code.LeakyBucket` | 2 → 5 | 1.00 |
| `code.parseGo` | `code.extractGoTypeName` | 2 → 13 | 1.20 |
| `code.extractGoFuncName` | `strings.TrimPrefix` | 2 → 13 | 1.80 |
| `code.extractTSFuncName` | `code.len` | 2 → 5 | 1.00 |
| `code.DistributedRateLimiter` | `code._InMemoryStore` | 7 → 8 | 1.00 |
| `strings.Index` | `code.extractGoTypeName` | 2 → 13 | 1.80 |
| `code.Lock` | `code._InMemoryStore` | 9 → 8 | 1.00 |
| `code.Lock` | `code.LeakyBucket` | 9 → 5 | 1.20 |
| `parser.go` | `code.extractGoFuncName` | 6 → 2 | 0.54 |
| `code.DistributedRateLimiter` | `rate_limiter.py` | 7 → 1 | 0.54 |
| `parser.go` | `code.CyclomaticComplexity` | 6 → 12 | 0.54 |
| `code.SlidingWindowCounter` | `rate_limiter.py` | 9 → 1 | 0.54 |
| `parser.go` | `code.parsePython` | 6 → 2 | 0.54 |
| `parser.go` | `code.extractGoTypeName` | 6 → 13 | 0.54 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `queue.ts` (degree 31) were refactored?
2. Is `rate_limiter.py` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'queue' and 'rate_limiter' share cross-module edges?
4. What is the relationship between `code.int` and `code.LeakyBucket` (surprising cross-community edge)?

