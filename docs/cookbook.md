# Cookbook — Real-World Usage Recipes

Practical recipes for common gleann workflows.

---

## 1. Index and Search a Go Codebase

```bash
# Build an index from a Go project
gleann index build myproject --docs ./

# Semantic search
gleann search myproject "how is authentication handled?"

# Ask a question (generates a synthesized answer via LLM)
gleann ask myproject "Explain the middleware chain in this codebase"
```

## 2. Index PDF Documentation

> **Requires:** [gleann-plugin-docs](plugin-install-guide.md)

```bash
# Index a folder of PDFs
gleann index build manuals --docs ./pdf-archive/

# Search across all documents
gleann search manuals "safety requirements for braking system"
```

## 3. Multi-Index Workspace

Index different content types separately, then search across all:

```bash
# Source code
gleann index build code --docs ./src/

# Architecture docs
gleann index build docs --docs ./docs/

# Meeting transcripts (requires gleann-plugin-sound)
gleann index build meetings --docs ./recordings/

# Search all indexes
gleann search code "rate limiter implementation"
gleann search docs "deployment architecture"
gleann search meetings "decision about database migration"
```

## 4. Chat with Your Codebase

Interactive conversation with context from your indexed code:

```bash
# Start a chat session
gleann chat myproject

# Inside chat:
> How does the payment service validate credit cards?
> What happens if the validation fails?
> Show me the error handling in that flow
```

## 5. Code Impact Analysis (Graph)

Analyze call graphs and dependency relationships:

```bash
# Build code graph
gleann graph build myproject

# Query relationships
gleann graph query myproject "what calls handlePayment?"
gleann graph query myproject "what does UserService depend on?"
```

## 6. REST API Server

Run gleann as an API server for integration with other tools:

```bash
# Start server
gleann serve --port 8080

# From another process:
curl -X POST http://localhost:8080/api/v1/search \
  -H "Content-Type: application/json" \
  -d '{"index": "myproject", "query": "authentication flow", "top_k": 5}'

curl -X POST http://localhost:8080/api/v1/ask \
  -H "Content-Type: application/json" \
  -d '{"index": "myproject", "question": "How does auth work?"}'
```

## 7. MCP Server (for LLM Tool Use)

Expose gleann as an MCP (Model Context Protocol) server so LLMs can search your code:

```bash
# Start MCP server on stdio
gleann mcp

# Or configure as an MCP server in your editor/agent config:
# {
#   "mcpServers": {
#     "gleann": {
#       "command": "gleann",
#       "args": ["mcp"]
#     }
#   }
# }
```

Available MCP tools:
- `gleann_search` — Semantic search across indexes
- `gleann_search_ids` — Compact search (5-10× fewer tokens), returns refs + peeks
- `gleann_fetch` — Hydrate full passage text for selected refs
- `gleann_get` — Citation lookup by persistent `"indexname:id"` ref
- `gleann_ask` — Ask questions about indexed content
- `gleann_list` — List available indexes
- `gleann_session_start` / `gleann_session_end` / `gleann_session_status` — track agent work sessions

## 8. CI/CD Integration

Index code on every push and run quality checks:

```yaml
# .github/workflows/gleann.yml
name: Code Intelligence
on: [push]

jobs:
  index:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Install gleann
        run: curl -sSL https://github.com/tevfik/gleann/releases/latest/download/install.sh | bash
      
      - name: Start Ollama
        run: |
          curl -fsSL https://ollama.ai/install.sh | sh
          ollama serve &
          sleep 5
          ollama pull bge-m3
      
      - name: Build Index
        run: gleann index build ${{ github.repository }} --docs ./ --embedding-model bge-m3
      
      - name: Verify Index
        run: gleann search ${{ github.repository }} "main entry point" --top-k 1
```

## 9. Docker Compose (Full Stack)

Run gleann + Ollama together with Docker:

```yaml
# docker-compose.yml
services:
  ollama:
    image: ollama/ollama:latest
    ports: ["11434:11434"]
    volumes: ["ollama_data:/root/.ollama"]

  gleann:
    image: gleann:latest
    ports: ["8080:8080"]
    environment:
      OLLAMA_URL: http://ollama:11434
    volumes: ["gleann_data:/root/.gleann"]
    command: ["serve", "--port", "8080"]
    depends_on: [ollama]

volumes:
  ollama_data:
  gleann_data:
```

```bash
docker compose up -d
# Wait for Ollama to be ready, then pull the models:
docker compose exec ollama ollama pull bge-m3
docker compose exec ollama ollama pull gemma3:4b
```

## 10. Environment-Based Configuration

Override settings without editing config files:

```bash
# Use a different Ollama server
OLLAMA_URL=http://gpu-server:11434 gleann ask myproject "explain auth"

# Use a specific model
GLEANN_CHAT_MODEL=llama3.1:8b gleann chat myproject

# Custom embedding model
GLEANN_EMBEDDING_MODEL=nomic-embed-text gleann index build myproject --docs ./

# Change data directory
GLEANN_DATA_DIR=/mnt/ssd/gleann gleann index build bigproject --docs ./
```

See [Environment Variables Reference](env-vars.md) for the complete list.

## 11. Rebuild a Stale Index

When source files change, rebuild the index to keep search results current:

```bash
# Full rebuild
gleann index build myproject --docs ./ --force

# Watch mode — automatically re-indexes on file changes (incremental)
# Only re-embeds changed files; falls back to full rebuild for deletions
gleann index watch myproject --docs ./

# Check index status
gleann index list
```

## 12. Multiple Ollama Models

Use different models for different tasks:

```bash
# Fast small model for casual chat
GLEANN_CHAT_MODEL=gemma3:4b gleann chat myproject

# Large model for complex questions
GLEANN_CHAT_MODEL=llama3.1:70b gleann ask myproject "refactor strategy for the payment module"

# Specialized coding model
GLEANN_CHAT_MODEL=qwen2.5-coder:7b gleann ask myproject "find potential bugs in the auth handler"
```

## 13. Long-term Memory

Store persistent knowledge that is automatically injected into every LLM query:

```bash
# Remember project-level facts once
gleann memory remember "This project uses hexagonal architecture"
gleann memory remember "Database: PostgreSQL 16, ORM: sqlx" --tag "stack"
gleann memory remember "Auth owner: Alice, payments owner: Bob" --tag "team"
gleann memory remember "Never use global state; prefer dependency injection" --tag "convention"

# From now on, every 'ask' and 'chat' receives this context automatically.
gleann ask myproject "Who should I talk to about the payment module?"
# → "According to your stored memory, payments are owned by Bob."

# Review what's in memory
gleann memory list
gleann memory context   # show exactly what the LLM receives

# Auto-summarize a conversation into medium-term memory
gleann memory summarize --last
gleann memory summarize --last --extract   # also extract individual facts

# Inside 'gleann chat':
# /remember Project is moving to microservices in Q3
# /forget  "hexagonal architecture"
# /memories
```

## 14. Memory-Augmented Code Review Workflow

Build a persistent coding assistant that remembers your team's conventions:

```bash
# 1. Index the codebase with code graph
gleann index build myrepo --docs ./src/ --graph

# 2. Store team conventions once
gleann memory remember "Always add error context with fmt.Errorf(\"...: %w\", err)" --tag "convention"
gleann memory remember "Unit tests live in _test.go files alongside source" --tag "convention"
gleann memory remember "Use table-driven tests with t.Run()" --tag "convention"

# 3. Review code with context (conventions auto-injected)
cat pkg/auth/handler.go | gleann ask myrepo "Review this file for convention violations"

# 4. Impact analysis
gleann graph callers "pkg/auth.ValidateToken" --index myrepo

# 5. End of day: summarize the conversation
gleann memory summarize --last
```

## 15. Batch Explore a Codebase (MCP)

Use the `gleann_batch_ask` MCP tool to research multiple aspects of a codebase in one round-trip:

```json
{
  "tool": "gleann_batch_ask",
  "arguments": {
    "questions": [
      "How is authentication handled?",
      "What database technology is used?",
      "How are errors propagated?",
      "What testing patterns are used?",
      "How is configuration loaded?"
    ],
    "index": "my-code",
    "top_k": 5,
    "concurrency": 5
  }
}
```

This runs all 5 questions concurrently and returns answers with per-question latency.

## 16. OpenAI-Compatible Integration

Use gleann as a drop-in replacement for OpenAI in any compatible tool:

```python
# Python — OpenAI SDK
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="unused")
resp = client.chat.completions.create(
    model="gleann/my-code",
    messages=[{"role": "user", "content": "Explain the auth flow"}],
)
print(resp.choices[0].message.content)
```

```bash
# curl — stream tokens
curl -N -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gleann/my-code",
    "messages": [{"role": "user", "content": "How does auth work?"}],
    "stream": true
  }'
```

## 17. Memory Blocks via REST API

```bash
# Store project knowledge
curl -X POST http://localhost:8080/api/blocks \
  -H 'Content-Type: application/json' \
  -d '{"content": "Uses PostgreSQL 16 with sqlx", "tier": "long", "tags": ["stack"]}'

# Search memories
curl 'http://localhost:8080/api/blocks/search?q=database'

# See what the LLM receives
curl http://localhost:8080/api/blocks/context

# Check storage stats
curl http://localhost:8080/api/blocks/stats
```
