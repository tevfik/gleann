# Model Context Protocol (MCP)

The Model Context Protocol is an open standard published by Anthropic in late
2024. It defines a uniform JSON-RPC interface that lets AI assistants discover
and invoke external tools, data sources, and prompts.

## Core Concepts

- **Server** — A process that exposes capabilities (tools, resources, prompts).
- **Client** — An AI assistant or IDE that connects to one or more servers.
- **Transport** — Typically `stdio` for local servers or `http+sse` for remote.

## Capabilities

| Capability | Purpose |
|------------|---------|
| Tools      | Callable functions the model can invoke with structured arguments. |
| Resources  | Read-only content (files, URLs, database rows) the model can fetch. |
| Prompts    | Reusable templated prompts the user can trigger. |

## Why MCP Matters

Before MCP every IDE and assistant invented its own plugin format. MCP
collapses that fragmentation: one server implementation works across Claude
Desktop, Cursor, Windsurf, Codex, OpenCode, Gemini CLI, GitHub Copilot CLI,
and any future client that speaks the protocol.

## Security Considerations

- MCP servers run with the privileges of the user that launched the client.
- Always review the tools a server exposes before enabling it.
- Prefer servers with explicit allow-lists for filesystem and network access.
