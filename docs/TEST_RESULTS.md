# Test Results Report — Gleann

**Date:** 2026-06-30  
**Run ID:** TEST-20260630-0842  
**Scope:** Gleann standalone + A2A protocol integration  
**Tester:** Automated CI / OpenClaw

---

## ✅ UNIT TESTS — Tüm Paketler

| Package | Coverage | Status |
|---------|----------|--------|
| `internal/a2a` | 87.0% | ✅ PASS |
| `internal/autosetup` | 84.1% | ✅ PASS |
| `internal/background` | 91.1% | ✅ PASS |
| `internal/embedding` | 76.9% | ✅ PASS |
| `internal/eventbus` | 92.0% | ✅ PASS |
| `internal/graph/community` | 81.6% | ✅ PASS |
| `internal/graph/indexer` | 77.0% | ✅ PASS |
| `internal/graph/kuzu` | 52.3% | ✅ PASS |
| `internal/graph/report` | 92.7% | ✅ PASS |
| `internal/graph/viz` | 69.2% | ✅ PASS |
| `internal/mcp` | 55.3% | ✅ PASS |
| `internal/multimodal` | 72.0% | ✅ PASS |
| `internal/packs` | 70.6% | ✅ PASS |
| `internal/packs/packshttp` | 83.1% | ✅ PASS |
| `internal/server` | 66.2% | ✅ PASS |
| `internal/service` | 64.7% | ✅ PASS |
| `internal/tui` | 64.9% | ✅ PASS (TestMain fix) |
| `internal/vault` | 82.5% | ✅ PASS |
| `pkg/analysis` | 82.9% | ✅ PASS |
| `pkg/backends` | 81.1% | ✅ PASS |
| `pkg/conversations` | 87.4% | ✅ PASS |
| `pkg/gleann` | 72.4% | ✅ PASS |
| `pkg/gleannignore` | 95.8% | ✅ PASS |
| `pkg/memory` | 79.2% | ✅ PASS |
| `pkg/retry` | 84.5% | ✅ PASS |
| `pkg/roles` | 77.4% | ✅ PASS |
| `pkg/wordwrap` | 92.5% | ✅ PASS |
| `tests/integration` | — | ✅ PASS (13/13) |

**Total: 59.6% coverage (statements)** — 26 paket, **0 FAIL**

---

## 🧪 E2E TEST SONUÇLARI

### Test Set 1: Unit Testler
| Sonuç | Detay |
|-------|-------|
| ✅ PASS | 26 paket, ~5dk toplam |
| Coverage | 59.6% total statements |
| TUI fix | 0.334s (önceden 90sn timeout → FAIL) |

### Test Set 2: Index List & Info
| Komut | Sonuç |
|-------|-------|
| `index list` | ✅ 3 index (gleann-src, test-index, codebase) |
| `index info codebase --json` | ✅ 3072 passages, diskann, 768dim |

### Test Set 3: Graph Query Tests
| Test | Sonuç |
|------|-------|
| Symbol Search (DefaultManager) | ✅ 1 result, depth=2 neighborhood (173 edges) |
| Dependencies | ✅ 2 deps (DefaultStore, NewManager) |
| Callers (DefaultStore) | ✅ 1 caller (DefaultManager) |
| Explain (blast radius) | ✅ 17 calls listed |

### Test Set 4: Risk + Community
| Test | Sonuç |
|------|-------|
| Risk Analysis (top 15) | ✅ context.Background en yüksek risk (0.6492) |
| Community Detection | ✅ 8080 nodes, 22037 edges, 789 communities, modularity 0.6297 |

### Test Set 5: Export Tests
| Format | Sonuç |
|--------|-------|
| GraphML | ✅ 84K satır export edildi |
| Cypher | ✅ 4MB Neo4j import dosyası |
| Code Map | ✅ PageRank top-10 listelendi |

### Test Set 6: Build + Vet
| Araç | Sonuç |
|------|-------|
| `go build -tags treesitter` | ✅ Clean (sadece tree-sitter lib warning) |
| `go vet -tags treesitter ./...` | ✅ Temiz |

### Test Set 7: REST API (Port 8080)
| Endpoint | Sonuç |
|----------|-------|
| `/health` | ✅ `{"status":"ok","engine":"gleann-go"}` |
| `/.well-known/agent-card.json` | ✅ 8 skill listeli A2A card |
| `/api/indexes` | ✅ 3 index JSON response |
| `/api/indexes/codebase/search` | ✅ 3 sonuç, skorlar sıralı |

### Test Set 8: Code Search + Graph Context
| Query | Sonuç |
|-------|-------|
| "memory store bbolt" --graph | ✅ 10 result, graph context enriched |

---

## 🔗 A2A PROTOCOL INTEGRATION

### A2A Client → Gleann Server
| Bileşen | Durum |
|---------|-------|
| Agent Card discovery | ✅ `/.well-known/agent-card.json` |
| Task submission | ✅ `/a2a/v1/message:send` |
| Task status polling | ✅ `/a2a/v1/tasks/{id}` |
| Exposed skills | ✅ 8 skills (Semantic Search, RAG Q&A, Code Graph, Memory, Community Detection, Repo Map, Risk Analysis, Multimodal) |

---

## 🔒 GÜVENLİK KONTROLÜ

| Kontrol | Sonuç |
|---------|-------|
| Hardcoded secrets | ✅ Yok (sadece test key'ler) |
| go vet temizliği | ✅ Temiz |
| API auth | ⚠️ Localhost-only (development acceptable) |
| Rate limiting | ✅ Token bucket yapılandırılmış |

---

## 📊 ÖZET

| Metrik | Değer |
|--------|-------|
| Toplam paket testi | 26 (Gleann Core Packages) |
| Başarılı testler | **100%** |
| Gleann coverage | **59.6%** |
| Build durumu | ✅ Temiz |
| Graph DB import | ✅ Hatasız (7625 sembol, 15326 edge) |
| REST API | ✅ Tüm endpoint'ler yanıt veriyor |
| A2A entegrasyon | ✅ Yapılandırma + card endpoint hazır |
| TUI test timeout | ✅ **ÇÖZÜLDÜ** (0.334s) |

---

**Report generated:** 2026-06-30 08:42 GMT+3  
**Status:** 🟢 ALL CLEAR — Teslim için hazır 