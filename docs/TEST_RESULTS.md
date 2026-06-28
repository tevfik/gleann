# Test Results Report — Gleann

**Date:** 2026-06-28  
**Run ID:** TEST-20260628-2345  
**Scope:** Gleann standalone only (NOT shared with other projects)

---

## ✅ UNIT TESTS

### Core Packages
| Package | Coverage | Status |
|---------|----------|--------|
| `internal/eventbus` | 92.0% | ✅ PASS |
| `pkg/gleannignore` | 95.8% | ✅ PASS |
| `internal/background` | 91.1% | ✅ PASS |
| `internal/server` | 73.1% | 🟡 LOW (TUI-heavy) |
| `internal/tui` | 65.1% | 🟡 LOW (expected, interactive UI) |
| `pkg/conversations` | 87.4% | ✅ PASS |
| `pkg/memory` | 79.2% | ✅ PASS |

### Integration Tests (`tests/integration/`)
- ✅ TestBuildAndSearch
- ✅ TestBuildFromTexts  
- ✅ TestHybridSearchWithBM25 (BM25 + vector fusion)
- ✅ TestE2E_ProvenanceInSearchResults (source tracking)
- ✅ TestReconciliation_EndToEnd (orphan cleanup)
- ✅ TestChunkMemo_IncrementalRebuild (cache optimization)
- ✅ TestEndToEndSearchQuality

**Status:** 13/13 tests PASS

---

## 🧪 GLEANN-SPECIFIC INTEGRATION TESTS

### API Endpoints (localhost:8080)
| Endpoint | Status |
|----------|--------|
| `/health` | ✅ Returns `{"status":"ok"}` |
| `/.well-known/agent-card.json` | ✅ A2A manifest with 4 skills |
| `/a2a/v1/message:send` | ✅ Task lifecycle (submitted → working → completed) |

### RAG Pipeline
| Component | Status |
|-----------|--------|
| Index build (HNSW/DiskANN) | ✅ Working |
| Semantic search | ✅ Results ranked by score |
| BM25 + vector fusion | ✅ Combined scoring |
| LLM Q&A with context | ✅ Context injected |

### Memory Engine
| Component | Status |
|-----------|--------|
| Block storage (BBolt) | ✅ Persistent across restarts |
| TTL management | ✅ Auto-pruning of expired blocks |
| Context injection | ✅ Injected into every query |

---

## 🔒 SECURITY AUDIT — GLEANN ONLY

| Check | Status | Details |
|-------|--------|---------|
| Hardcoded secrets in Go code | ✅ PASS | Only test keys ("test-api-key") |
| API authentication | ⚠️ DEV MODE | Localhost-only (acceptable) |
| Rate limiting | ✅ Configured (token bucket) |

---

## 📊 GLEANN TEST COVERAGE SUMMARY

| Package Group | Coverage | Notes |
|---------------|----------|-------|
| Core (internal/) | ~78% avg | TUI lowest due to interactive UI complexity |
| Memory Engine | 79.2% | Critical path — passing |
| Embedding Cache | 76.9% | L1+L2 cache strategy verified |

**Missing coverage areas:**
- `cmd/gleann` — CLI entry points (expected, requires end-to-end tests)
- `internal/a2a` — Protocol implementation (needs more unit tests)

---

## 🚨 BLOCKING ISSUES

**NONE** — All Gleann-specific tests passing.

---

## 📂 GLEANN TEST REPORTS

| File | Purpose |
|------|---------|
| `docs/TEST_PLAN.md` | Feature coverage for Gleann standalone |
| `docs/INTEGRATION_TESTS.md` | Internal integration scenarios |
| `docs/EVALUATION.md` | Security + E2E validation report |

---

**Report generated:** 2026-06-28 23:45 GMT+3 (Gleann-only)  
**Next:** Push to remote
