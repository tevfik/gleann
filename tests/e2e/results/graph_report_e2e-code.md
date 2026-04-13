# Graph Report: e2e-code

Generated: 2026-04-13 19:38:34

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 139 |
| Edges | 189 |
| Communities | 26 |
| Modularity (Q) | 0.5444 |
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
| 10 | `code.DistributedRateLimiter` | class | 1 | 6 | 7 |
| 11 | `code.parsePython` | function | 2 | 5 | 7 |
| 12 | `code.extractGoFuncName` | function | 2 | 5 | 7 |
| 13 | `code.generateId` | function | 2 | 5 | 7 |
| 14 | `code.PersistentQueue_part4` | class | 1 | 5 | 6 |
| 15 | `strings.TrimSpace` | function | 6 | 0 | 6 |
| 16 | `code.extractTSFuncName` | function | 2 | 3 | 5 |
| 17 | `code.Parser.Parse` | method | 1 | 4 | 5 |
| 18 | `code.TokenBucket_part1` | class | 1 | 4 | 5 |
| 19 | `code.monotonic` | function | 5 | 0 | 5 |
| 20 | `code.TokenBucket_part2` | class | 1 | 4 | 5 |

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

- `code.PersistentQueue_part3`
- `code.pow`
- `code.random`
- `code.reEnqueueExpiredMessages`
- `code.receive`
- `code.setInterval`

### Community 3: code (6 nodes, cohesion=0.167)

- `code.from`
- `code.generateId`
- `code.getRandomValues`
- `code.join`
- `code.padStart`
- `code.toString`

### Community 4: parser (6 nodes, cohesion=0.050)

- `code.LangGo`
- `code.Language`
- `code.New`
- `code.Parser`
- `code.Symbol`
- `parser.go`

### Community 5: code (6 nodes, cohesion=0.167)

- `code.DistributedRateLimiter`
- `code.expire`
- `code.incr`
- `code.int`
- `code.max`
- `code.time`

### Community 6: code (6 nodes, cohesion=0.167)

- `code.LeakyBucket`
- `code._leak`
- `code.deque`
- `code.len`
- `code.popleft`
- `code.range`

### Community 7: code (5 nodes, cohesion=0.200)

- `code.PersistentQueue_part2`
- `code.add`
- `code.entries`
- `code.has`
- `code.push`

### Community 8: code (5 nodes, cohesion=0.200)

- `code._InMemoryStore`
- `code._evict_expired`
- `code.get`
- `code.items`
- `code.pop`

### Community 9: code (4 nodes, cohesion=0.250)

- `code.TokenBucket_part1`
- `code._refill`
- `code.field`
- `code.min`

### Community 10: code (4 nodes, cohesion=0.250)

- `code.PersistentQueue_part4`
- `code.clearInterval`
- `code.delete`
- `code.emit`

### Community 11: code (3 nodes, cohesion=0.333)

- `code.TokenBucket_part2`
- `code.monotonic`
- `code.sleep`

### Community 12: parser (3 nodes, cohesion=0.500)

- `code.extractTSFuncName`
- `code.parseTypeScript`
- `strings.HasPrefix`

### Community 13: code (3 nodes, cohesion=0.333)

- `code.PersistentQueue_part1`
- `code.now`
- `code.set`

### Community 14: code (3 nodes, cohesion=0.333)

- `code.SlidingWindowCounter_part2`
- `code._advance_window_if_needed`
- `code._estimate_count`

### Community 15: parser (3 nodes, cohesion=0.500)

- `code.extractPyName`
- `code.parsePython`
- `strings.TrimSpace`

### Community 16: strings (3 nodes, cohesion=0.333)

- `code.parseGo`
- `strings.Contains`
- `strings.Split`

### Community 17: code (2 nodes, cohesion=0.500)

- `code.Lock`
- `code.SlidingWindowCounter_part1`

### Community 18: parser (2 nodes, cohesion=0.500)

- `code.extractGoFuncName`
- `strings.IndexAny`

### Community 19: parser (2 nodes, cohesion=0.500)

- `code.CountSymbols`
- `code.make`

### Community 20: parser (2 nodes, cohesion=0.500)

- `code.CyclomaticComplexity`
- `strings.Count`

### Community 21: parser (2 nodes, cohesion=0.500)

- `code.extractTSClassName`
- `strings.Index`

### Community 22: parser (2 nodes, cohesion=0.500)

- `code.Parser.Parse`
- `fmt.Errorf`

### Community 23: parser (2 nodes, cohesion=0.500)

- `code.extractGoTypeName`
- `strings.TrimPrefix`

### Community 24: queue (2 nodes, cohesion=0.500)

- `code.CircularBuffer`
- `code.fill`

### Community 25: parser (2 nodes, cohesion=0.500)

- `code.FilterByKind`
- `code.append`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `code.PersistentQueue_part3` | `code.set` | 2 → 13 | 1.20 |
| `code.extractGoFuncName` | `code.parseGo` | 18 → 16 | 1.00 |
| `code.extractTSClassName` | `strings.TrimSpace` | 21 → 15 | 1.80 |
| `code.PersistentQueue_part1` | `code.emit` | 13 → 10 | 1.00 |
| `code.parseTypeScript` | `strings.Contains` | 12 → 16 | 1.80 |
| `code.parseTypeScript` | `strings.TrimSpace` | 12 → 15 | 1.50 |
| `code.parseTypeScript` | `strings.Split` | 12 → 16 | 1.80 |
| `code.parseTypeScript` | `code.Parser.Parse` | 12 → 22 | 1.80 |
| `code.parseTypeScript` | `code.append` | 12 → 25 | 1.20 |
| `code.TokenBucket_part2` | `code._refill` | 11 → 9 | 1.00 |
| `code._InMemoryStore` | `code.Lock` | 8 → 17 | 1.20 |
| `code.DistributedRateLimiter` | `code._InMemoryStore` | 5 → 8 | 1.00 |
| `strings.IndexAny` | `code.extractTSFuncName` | 18 → 12 | 1.80 |
| `code.len` | `code.extractTSFuncName` | 6 → 12 | 1.20 |
| `code.parsePython` | `code.Parser.Parse` | 15 → 22 | 1.80 |
| `code.LeakyBucket` | `code.int` | 6 → 5 | 1.00 |
| `code.LeakyBucket` | `code.monotonic` | 6 → 11 | 1.20 |
| `code.LeakyBucket` | `code.append` | 6 → 25 | 1.20 |
| `code.LeakyBucket` | `code.Lock` | 6 → 17 | 1.20 |
| `code.parsePython` | `strings.Split` | 15 → 16 | 1.50 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `queue.ts` (degree 39) were refactored?
2. Is `rate_limiter.py` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'queue' and 'rate_limiter' share cross-module edges?
4. What is the relationship between `code.PersistentQueue_part3` and `code.set` (surprising cross-community edge)?

