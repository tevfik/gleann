# Graph Report: e2e-code

Generated: 2026-08-03 13:41:37

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 128 |
| Edges | 164 |
| Communities | 21 |
| Modularity (Q) | 0.6063 |
| God Nodes | 19 |
| Cross-Community Edges | 20 |

**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.

## God Nodes (High-Degree Hubs)

These symbols have an unusually high number of connections, making them central to the codebase.

| Rank | Symbol | Kind | In° | Out° | Total° |
|------|--------|------|-----|------|--------|
| 1 | `queue.ts` | file | 0 | 31 | 31 |
| 2 | `rate_limiter.py` | file | 0 | 24 | 24 |
| 3 | `code.PersistentQueue` | class | 1 | 16 | 17 |
| 4 | `parser.go` | file | 0 | 17 | 17 |
| 5 | `code.LeakyBucket` | class | 1 | 9 | 10 |
| 6 | `code.parseGo` | function | 2 | 7 | 9 |
| 7 | `code.parseTypeScript` | function | 2 | 7 | 9 |
| 8 | `code._InMemoryStore` | class | 2 | 6 | 8 |
| 9 | `code.parsePython` | function | 2 | 5 | 7 |
| 10 | `code.extractGoFuncName` | function | 2 | 5 | 7 |
| 11 | `code.generateId` | function | 2 | 5 | 7 |
| 12 | `code.DistributedRateLimiter` | class | 1 | 6 | 7 |
| 13 | `code.TokenBucket` | class | 1 | 5 | 6 |
| 14 | `strings.TrimSpace` | function | 6 | 0 | 6 |
| 15 | `strings.HasPrefix` | function | 5 | 0 | 5 |
| 16 | `code.Parser.Parse` | method | 1 | 4 | 5 |
| 17 | `code.append` | function | 5 | 0 | 5 |
| 18 | `code.extractTSFuncName` | function | 2 | 3 | 5 |
| 19 | `code.SlidingWindowCounter` | class | 1 | 4 | 5 |

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

### Community 2: code (15 nodes, cohesion=0.067)

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

### Community 3: parser (6 nodes, cohesion=0.050)

- `code.LangGo`
- `code.Language`
- `code.New`
- `code.Parser`
- `code.Symbol`
- `parser.go`

### Community 4: code (6 nodes, cohesion=0.167)

- `code.LeakyBucket`
- `code._leak`
- `code.deque`
- `code.len`
- `code.popleft`
- `code.range`

### Community 5: code (6 nodes, cohesion=0.167)

- `code.DistributedRateLimiter`
- `code.expire`
- `code.incr`
- `code.int`
- `code.max`
- `code.time`

### Community 6: code (6 nodes, cohesion=0.167)

- `code.from`
- `code.generateId`
- `code.getRandomValues`
- `code.join`
- `code.padStart`
- `code.toString`

### Community 7: code (6 nodes, cohesion=0.167)

- `code.TokenBucket`
- `code._refill`
- `code.field`
- `code.min`
- `code.monotonic`
- `code.sleep`

### Community 8: code (5 nodes, cohesion=0.200)

- `code._InMemoryStore`
- `code._evict_expired`
- `code.get`
- `code.items`
- `code.pop`

### Community 9: code (4 nodes, cohesion=0.250)

- `code.Lock`
- `code.SlidingWindowCounter`
- `code._advance_window_if_needed`
- `code._estimate_count`

### Community 10: parser (3 nodes, cohesion=0.500)

- `code.extractPyName`
- `code.parsePython`
- `strings.TrimSpace`

### Community 11: parser (3 nodes, cohesion=0.500)

- `code.extractTSFuncName`
- `code.parseTypeScript`
- `strings.HasPrefix`

### Community 12: strings (3 nodes, cohesion=0.333)

- `code.parseGo`
- `strings.Contains`
- `strings.Split`

### Community 13: strings (2 nodes, cohesion=0.500)

- `code.extractGoFuncName`
- `strings.IndexAny`

### Community 14: parser (2 nodes, cohesion=0.500)

- `code.extractTSClassName`
- `strings.Index`

### Community 15: parser (2 nodes, cohesion=0.500)

- `code.FilterByKind`
- `code.append`

### Community 16: parser (2 nodes, cohesion=0.500)

- `code.Parser.Parse`
- `fmt.Errorf`

### Community 17: queue (2 nodes, cohesion=0.500)

- `code.CircularBuffer`
- `code.fill`

### Community 18: parser (2 nodes, cohesion=0.500)

- `code.extractGoTypeName`
- `strings.TrimPrefix`

### Community 19: parser (2 nodes, cohesion=0.500)

- `code.CountSymbols`
- `code.make`

### Community 20: parser (2 nodes, cohesion=0.500)

- `code.CyclomaticComplexity`
- `strings.Count`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `code.parseTypeScript` | `strings.TrimSpace` | 11 → 10 | 1.50 |
| `code.parseTypeScript` | `code.extractTSClassName` | 11 → 14 | 1.00 |
| `code.PersistentQueue` | `code.generateId` | 2 → 6 | 1.20 |
| `code.append` | `code.LeakyBucket` | 15 → 4 | 1.20 |
| `code.parseGo` | `code.Parser.Parse` | 12 → 16 | 1.80 |
| `code.parseGo` | `strings.HasPrefix` | 12 → 11 | 1.50 |
| `code.parsePython` | `strings.Split` | 10 → 12 | 1.50 |
| `code.parseGo` | `code.append` | 12 → 15 | 1.00 |
| `code.parseGo` | `strings.TrimSpace` | 12 → 10 | 1.50 |
| `code.parseGo` | `code.extractGoTypeName` | 12 → 18 | 1.20 |
| `code.parseGo` | `code.extractGoFuncName` | 12 → 13 | 1.00 |
| `code.parsePython` | `strings.HasPrefix` | 10 → 11 | 1.50 |
| `code.parseTypeScript` | `code.Parser.Parse` | 11 → 16 | 1.80 |
| `code.parseTypeScript` | `strings.Contains` | 11 → 12 | 1.50 |
| `code.parseTypeScript` | `code.append` | 11 → 15 | 1.20 |
| `code.parseTypeScript` | `strings.Split` | 11 → 12 | 1.50 |
| `code.PersistentQueue` | `code.get` | 2 → 8 | 1.20 |
| `strings.TrimSpace` | `code.extractTSClassName` | 10 → 14 | 1.80 |
| `code.int` | `code.LeakyBucket` | 5 → 4 | 1.00 |
| `code.Parser.Parse` | `code.parsePython` | 16 → 10 | 1.80 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `queue.ts` (degree 31) were refactored?
2. Is `rate_limiter.py` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'queue' and 'rate_limiter' share cross-module edges?
4. What is the relationship between `code.parseTypeScript` and `strings.TrimSpace` (surprising cross-community edge)?

