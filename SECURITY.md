# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Gleann, please report it responsibly:

1. **Do NOT open a public GitHub issue** for security vulnerabilities.
2. Email the maintainer directly at the address listed on the GitHub profile.
3. Include a detailed description of the vulnerability, steps to reproduce, and potential impact.

We will acknowledge receipt within 48 hours and aim to issue a fix within 7 days for critical issues.

## Security Considerations

Gleann is designed as a **local-first** tool. Key security notes:

- **Local operation**: By default, all data stays on your machine. Embeddings are computed locally via Ollama.
- **API keys**: When using cloud providers (OpenAI, Anthropic), API keys are stored in `~/.gleann/config.json`. Ensure appropriate file permissions (`chmod 600`).
- **REST server**: The `gleann serve` command binds to `localhost:8080` by default. If exposed to a network, be aware there is no built-in authentication.
- **MCP server**: Runs over stdio (not network), so it is inherently local.
- **Plugin system**: Plugins run as local HTTP servers. Only install plugins you trust.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest  | Yes       |
| < 1.0   | Best effort |
