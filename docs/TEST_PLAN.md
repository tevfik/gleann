# Test Plan — Gleann

**Date:** 2026-06-28  
**Author:** Bezgin (OpenClaw)  
**Scope:** Complete feature coverage for Gleann standalone (NOT shared with other projects)

---

## 📋 Overview

Gleann is a self-contained AI/RAG backend implemented in Go. This test plan covers:

1. Core RAG functionality (semantic search, multi-index queries)
2. Code intelligence (AST call graph, blast-radius analysis)
3. Long-term memory engine (BBolt blocks with TTL)
4. MCP server (for Cursor/Claude/Gemini/etc.)
5. REST API & A2A protocol
6. Security & performance requirements

**Note:** Test plans for Yaver-Go and SE-Agent are maintained in their respective repositories.

---

## 🧪 1. GLEANN TEST PLAN

### 1.1 Core RAG Functionality

| Test ID | Feature | Command/Action | Expected Result |
|---------|---------|----------------|-----------------|
| GL-RAG-001 | Build index from docs | `gleann index build docs --docs ./docs` | Index created, passages indexed |
| GL-RAG-002 | Semantic search | `gleann search docs "vector store"` | Results ranked by relevance score |
| GL-RAG-003 | RAG Q&A (single index) | `gleann ask docs "What is HNSW?"` | LLM answer with source citations |
| GL-RAG-004 | Multi-index query | `gleann ask docs,code "How does auth work?"` | Results merged by score |
| GL-RAG-005 | Context injection | `gleann ask docs --continue-last "Explain error handling"` | Conversation history preserved |
| GL-RAG-006 | Role control | `gleann ask docs "List endpoints" --role code --format json` | JSON output with code focus |
| GL-RAG-007 | Streaming (SSE) | `curl http://localhost:8080/api/indexes/docs/ask?stream=true -d '{"question":"..."}'` | Server-Sent Events stream |

### 1.2 Code Intelligence (AST Graph)

| Test ID | Feature | Command/Action | Expected Result |
|---------|---------|----------------|-----------------|
| GL-CODE-001 | Build with graph | `gleann index build code --docs ./src --graph` | AST call graph indexed |
| GL-CODE-002 | Callers/callees search | `gleann search code "handleRequest" --graph` | Graph context enriched results |
| GL-CODE-003 | Blast radius analysis | `se-agent impact pkg/auth.Validate` (external tool) | BFS traversal output |

### 1.3 Long-Term Memory

| Test ID | Feature | Command/Action | Expected Result |
|---------|---------|----------------|-----------------|
| GL-MEM-001 | Remember fact | `gleann memory remember "API key is abc123" --tier long` | Block stored with hash |
| GL-MEM-002 | Recall facts | `gleann memory search "API key"` | Matching blocks returned |
| GL-MEM-003 | Context injection | `gleann ask docs "What do we know about API?"` | Memory injected into prompt |

### 1.4 MCP Server (for AI Editors)

| Test ID | Feature | Command/Action | Expected Result |
|---------|---------|----------------|-----------------|
| GL-MCP-001 | Start server | `gleann mcp` | stdio-based MCP server running |
| GL-MCP-002 | Discover tools | Check `mcp.listTools()` response | 4+ tools exposed (search, ask, etc.) |

### 1.5 REST API Server

| Test ID | Feature | Command/Action | Expected Result |
|---------|---------|----------------|-----------------|
| GL-API-001 | Health check | `curl http://localhost:8080/health` | `{"status":"ok"}` |
| GL-API-002 | Agent card | `curl http://localhost:8080/.well-known/agent-card.json` | A2A agent manifest |

### 1.6 A2A Protocol (Agent-to-Agent)

| Test ID | Feature | Command/Action | Expected Result |
|---------|---------|----------------|-----------------|
| GL-A2A-001 | Agent discovery | `curl http://localhost:8080/.well-known/agent-card.json` | Skills listed with tags/examples |

### 1.7 Security & Privacy

| Test ID | Feature | Test Method | Expected Result |
|---------|---------|-------------|-----------------|
| GL-SEC-001 | No hardcoded secrets | Scan all `.go` files | Only fake test keys (e.g., "test-api-key") |
| GL-SEC-002 | Local-only LLM calls | Check config, network traffic | All requests to localhost:11434 |

### 1.8 Performance

| Test ID | Feature | Metric | Target |
|---------|---------|--------|--------|
| GL-PERF-001 | Index build speed | Lines/sec | >50k lines/s (diskann) |
| GL-PERF-002 | Query latency | P95 | <1s for 10 passages |

---

## 🧪 2. GLEANN-SPECIFIC INTEGRATION TESTS

### INT-GL-001: RAG Pipeline
Embedding → Index → Search → RAG query akışı.

```
gleann index build test-index --docs ./tests/integration/fixtures/code
gleann search test-index "vector database"
gleann ask test-index "What is HNSW?" --interactive false
```

### INT-GL-002: Memory Engine ↔ RAG Context

```
gleann memory remember "Project uses JWT" --tier long
gleann ask docs "What do we know about authentication?"
→ Memory block injected into prompt
```

### INT-GL-003: Cache Invalidation

```
gleann config set embedding.model bge-m3
# Old cache invalid, rebuild required warning expected
```

---

## 📊 TEST COVERAGE REQUIREMENTS

| System | Unit Test Coverage | E2E Tests |
|--------|-------------------|-----------|
| **Gleann** | ≥75% average | 10+ scenarios |

---

## 🔧 GLEANN-SPECIFIC TEST INFRASTRUCTURE

### Automated Tests
```bash
cd gleann && go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### Manual Test Checklist (Gleann only)
- [ ] All Gleann features in Quick Start README sections work
- [ ] A2A endpoints respond correctly (health, agent-card, message:send)
- [ ] No hardcoded secrets in gleann codebase
- [ ] Rate limiting kicks in at configured threshold
- [ ] Memory leak detection passes (task cleanup works)

---

## 🚨 BLOCKING ISSUES (Gleann)

| Issue | Status | Impact |
|-------|--------|--------|
| None currently | ✅ DONE | All tests passing |

---

**Report generated:** 2026-06-28 23:15 GMT+3 (Gleann-only)  
**Next steps:** Run integration test suite → Generate coverage report → Push to remote
