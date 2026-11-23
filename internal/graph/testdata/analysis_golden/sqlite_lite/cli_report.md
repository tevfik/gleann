# Graph Report: fixture

Generated: <TIMESTAMP>
Source: `<FIXTURE>`

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 51 |
| Edges | 62 |
| Communities | 14 |
| Modularity (Q) | 0.4798 |
| God Nodes | 9 |
| Cross-Community Edges | 20 |

**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.

## God Nodes (High-Degree Hubs)

These symbols have an unusually high number of connections, making them central to the codebase.

| Rank | Symbol | Kind | In° | Out° | Total° |
|------|--------|------|-----|------|--------|
| 1 | `service.go` | file | 0 | 9 | 9 |
| 2 | `store.go` | file | 0 | 7 | 7 |
| 3 | `fixture.main` | function | 1 | 5 | 6 |
| 4 | `service.Service.Save` | method | 4 | 2 | 6 |
| 5 | `app.py` | file | 0 | 6 | 6 |
| 6 | `service.Service.Load` | method | 4 | 1 | 5 |
| 7 | `service.Service.Report` | method | 3 | 2 | 5 |
| 8 | `store.Store` | interface | 5 | 0 | 5 |
| 9 | `app.ts` | file | 0 | 5 | 5 |

> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.

## Communities

Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.

### Community 0: app (7 nodes, cohesion=0.043)

- `py.Registry`
- `py.__init__`
- `py.add`
- `py.build_registry`
- `py.describe`
- `py.main`
- `app.py`

### Community 1: app (6 nodes, cohesion=0.050)

- `ts.Shape`
- `ts.Square`
- `ts.describeAll`
- `ts.main`
- `ts.totalArea`
- `app.ts`

### Community 2: main (5 nodes, cohesion=0.060)

- `rs.Counter`
- `rs.main`
- `rs.run_all`
- `rs.util`
- `main.rs`

### Community 3: service (5 nodes, cohesion=0.060)

- `service.Service`
- `service.emptyError`
- `service.emptyError.Error`
- `service.errEmpty`
- `service.go`

### Community 4: service (4 nodes, cohesion=0.317)

- `service.Service.Load`
- `service.Service.Report`
- `store.MemStore`
- `store.Store`

### Community 5: main (3 nodes, cohesion=0.267)

- `fixture.main`
- `fixture.run`
- `main.go`

### Community 6: util (3 nodes, cohesion=0.100)

- `py.clean`
- `py.join_words`
- `util.py`

### Community 7: util (3 nodes, cohesion=0.100)

- `rs.clamp`
- `rs.double`
- `util.rs`

### Community 8: service_test (3 nodes, cohesion=0.217)

- `service.New`

### Community 9: store (3 nodes, cohesion=0.100)

- `store.MemStore.Get`
- `store.MemStore.Keys`
- `store.go`

### Community 10: util (3 nodes, cohesion=0.100)

- `ts.pluralize`
- `ts.slug`
- `util.ts`

### Community 11: service (2 nodes, cohesion=0.500)

- `service.Service.Save`
- `service.validate`

### Community 12: service_test (2 nodes, cohesion=0.500)

- `store.NewMemStore`

### Community 13: store (2 nodes, cohesion=0.500)

- `store.MemStore.Put`
- `store.normalize`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `fixture.main` | `service.New` | 5 → 8 | 1.50 |
| `fixture.main` | `service.Service.Report` | 5 → 4 | 1.50 |
| `fixture.main` | `service.Service.Save` | 5 → 11 | 1.80 |
| `fixture.main` | `store.NewMemStore` | 5 → 12 | 1.80 |
| `fixture.run` | `service.Service.Load` | 5 → 4 | 1.50 |
| `service.Service.Save` | `store.Store` | 11 → 4 | 1.80 |
| `service.New` | `service.go` | 8 → 3 | 0.54 |
| `service.Service.Load` | `service.go` | 4 → 3 | 0.45 |
| `service.Service.Report` | `service.go` | 4 → 3 | 0.45 |
| `service.Service.Save` | `service.go` | 11 → 3 | 0.54 |
| `service.validate` | `service.go` | 11 → 3 | 0.54 |
| `store.MemStore` | `store.go` | 4 → 9 | 0.54 |
| `store.MemStore.Put` | `store.go` | 13 → 9 | 0.54 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `service/service.go` (degree 9) were refactored?
2. Is `store/store.go` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'app' and 'app' share cross-module edges?
4. What is the relationship between `fixture.main` and `service.New` (surprising cross-community edge)?

