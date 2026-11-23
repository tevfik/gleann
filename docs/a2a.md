# A2A Protocol (Agent-to-Agent)

> **Status: experimental.** Tasks are single-turn (one message in, one result out) and there is no streaming. Multi-turn tasks (`input-required` and resume) are specified in [RFC 007](plans/007-a2a-multi-turn.md) and not implemented yet. The endpoints may change between minor versions.

Gleann implements [Google's A2A protocol](https://google.github.io/A2A/) for agent discovery and inter-agent communication. This allows other AI agents, orchestrators, and tools to discover gleann's capabilities and send it tasks via a standard HTTP+JSON interface.

## Overview

When `gleann serve` starts, three A2A endpoints become available:

| Endpoint | Description |
|----------|-------------|
| `GET /.well-known/agent-card.json` | Agent Card — describes gleann's skills and capabilities |
| `POST /a2a/v1/message:send` | Send a task to gleann (auto-routed to the best skill) |
| `GET /a2a/v1/tasks/{id}` | Check task status and retrieve results |

## Authentication and scopes

A2A endpoints use the same bearer tokens as the REST API (see [api.md](api.md#authentication)):
send `Authorization: Bearer <token>` on every `message:send` call (the agent card advertises
the `gleannBearer` scheme).

- A **query** token can use the read-only skills: semantic search, RAG ask, code graph
  analysis, memory recall/search, and multimodal analysis of *attached* files.
- An **admin** token is additionally required to **store memory blocks**
  (`memory-management` messages containing `remember`, `hatırla` or `store`) and to
  **analyze a server-local file path** with `multimodal-analyze` (the query is then a path
  on the server, held to `allowed_roots`).

With a query token these privileged operations fail the task (`TASK_STATE_FAILED`) with a
message naming the admin scope; nothing is written and no local file is read. When the
server runs with `auth: off` (loopback only), all skills are available.

## Agent Card

The Agent Card at `/.well-known/agent-card.json` advertises four built-in skills:

| Skill | Keywords | Description |
|-------|----------|-------------|
| `semantic-search` | search, find, look up, where | Semantic search across indexed documents |
| `ask-rag` | what, why, how, explain, summarize | RAG-based Q&A with source citations |
| `code-analysis` | callers, callees, impact, symbol, function | AST graph queries (format-2 code graph, every build) |
| `memory-management` | remember, recall, forget, memory | Store and retrieve long-term memory blocks |

## Sending Messages

```bash
# Ask gleann a question (auto-routed to ask-rag skill)
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $GLEANN_API_TOKEN" \
  -d '{
    "message": {
      "role": "user",
      "parts": [{"text": "How does the authentication system work?"}]
    }
  }'

# Explicitly target a specific skill
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $GLEANN_API_TOKEN" \
  -d '{
    "message": {
      "role": "user",
      "parts": [{"text": "validateToken"}],
      "metadata": {"skill": "code-analysis"}
    }
  }'

# Store a memory (requires an admin token; a query token may only recall/search)
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $GLEANN_API_TOKEN" \
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
