# Entegrasyon Testleri - Gleann

**Date:** 2026-06-28  
**Author:** Bezgin  
**Scope:** Internal Gleann integration points only (NOT shared with Yaver-Go or SE-Agent)

---

## 🧪 GLEANN İÇ ENTREGRASYON TESTLERİ

### INT-GL-001: RAG Pipeline
**Amaç:** Embedding → Index → Search → RAG query akışı.

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

### INT-GL-003: AST Graph ↔ Code Search

```
gleann index build code --docs ./src --graph
gleann search code "handleRequest" --graph
→ Callers/callees enriched results
```

### INT-GL-004: MCP Server ↔ A2A Protocol
**Amaç:** Her iki protokol de aynı LLM/Embedding backend'i kullanmalı.

```
gleann mcp --addr :8079 &
gleann serve --port 8080 &
# Both use same Ollama endpoint (localhost:11434)
```

### INT-GL-005: Cache Invalidation

```
gleann config set embedding.model bge-m3
# Old cache invalid, rebuild required warning expected
```

---

## 📊 GLEANN-SPECIFIC TEST SCENARIOS

### Scenario 1: Fresh Install Flow
```
gleann setup --auto
→ Detects Ollama → Pulls models → Builds initial index
→ All components working end-to-end
```

### Scenario 2: Long-Term Memory Retention
```
1. gleann memory remember "Project X uses JWT" --tier long
2. Stop/start gleann serve
3. gleann memory search "JWT"
   → Memory preserved across restarts
```

---

## 🚨 BLOCKING ISSUES

**NONE** — All tests passing.
