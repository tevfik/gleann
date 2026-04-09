# A2A Protocol (Agent-to-Agent)

Gleann implements [Google's A2A protocol](https://google.github.io/A2A/) for agent discovery and inter-agent communication. This allows other AI agents, orchestrators, and tools to discover gleann's capabilities and send it tasks via a standard HTTP+JSON interface.

## Overview

When `gleann serve` starts, three A2A endpoints become available:

| Endpoint | Description |
|----------|-------------|
| `GET /.well-known/agent-card.json` | Agent Card — describes gleann's skills and capabilities |
| `POST /a2a/v1/message:send` | Send a task to gleann (auto-routed to the best skill) |
| `GET /a2a/v1/tasks/{id}` | Check task status and retrieve results |

## Agent Card

The Agent Card at `/.well-known/agent-card.json` advertises four built-in skills:

| Skill | Keywords | Description |
|-------|----------|-------------|
| `semantic-search` | search, find, look up, where | Semantic search across indexed documents |
| `ask-rag` | what, why, how, explain, summarize | RAG-based Q&A with source citations |
| `code-analysis` | callers, callees, impact, symbol, function | AST graph queries (requires treesitter build) |
| `memory-management` | remember, recall, forget, memory | Store and retrieve long-term memory blocks |

## Sending Messages

```bash
# Ask gleann a question (auto-routed to ask-rag skill)
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "role": "user",
      "parts": [{"text": "How does the authentication system work?"}]
    }
  }'

# Explicitly target a specific skill
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "role": "user",
      "parts": [{"text": "validateToken"}],
      "metadata": {"skill": "code-analysis"}
    }
  }'

# Store a memory
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "role": "user",
      "parts": [{"text": "remember: the deploy key is stored in vault"}]
    }
  }'
```

## Skill Routing

Messages are automatically routed to the best skill using priority-ordered keyword matching:

1. **code-analysis** — triggered by: callers, callees, impact, symbol, function, method, class
2. **memory-management** — triggered by: remember, recall, forget, memory
3. **semantic-search** — triggered by: search, find, look up, where
4. **ask-rag** — fallback for all other questions

You can bypass auto-routing by setting `metadata.skill` in the message.

## Configuration

| Setting | Config Key | Env Var | Default |
|---------|-----------|---------|---------|
| Enable/disable A2A | `a2a_enabled` | `GLEANN_A2A_ENABLED` | `true` |
| Agent Card base URL | `a2a_base_url` | `GLEANN_A2A_BASE_URL` | auto-detected from server address |

```json
// ~/.gleann/config.json
{
  "a2a_enabled": true,
  "a2a_base_url": "https://my-gleann.example.com"
}
```

## Integration with AI Orchestrators

Any A2A-compatible orchestrator can discover and use gleann:

```python
# Python example — discover gleann
import requests

card = requests.get("http://localhost:8080/.well-known/agent-card.json").json()
print(f"Agent: {card['name']} v{card['version']}")
for skill in card['skills']:
    print(f"  - {skill['id']}: {skill['description']}")

# Send a search query
resp = requests.post("http://localhost:8080/a2a/v1/message:send", json={
    "message": {
        "role": "user",
        "parts": [{"text": "find all authentication-related code"}]
    }
})
task = resp.json()
print(task["status"]["message"])  # The search results
```

## Task Lifecycle

Tasks follow the A2A lifecycle: `submitted` → `working` → `completed` (or `failed`).

All current skills execute synchronously (the response contains the final result immediately). The task ID can be used to retrieve results later via `GET /a2a/v1/tasks/{id}`.
