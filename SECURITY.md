# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Gleann, please report it
**privately**:

1. **Do NOT open a public GitHub issue** for security findings.
2. Email the maintainer directly at the address listed on the GitHub
   profile of [@tevfik](https://github.com/tevfik).
3. Include a detailed description of the vulnerability, steps to
   reproduce, and potential impact.

We will acknowledge receipt within **48 hours** and aim to issue a fix
within **7 days** for critical issues.

## Security Considerations

Gleann is designed as a **local-first** tool. Key security notes:

- **Local operation**: By default, all data stays on your machine.
  Embeddings are computed locally via Ollama.
- **API keys**: When using cloud providers (OpenAI, Anthropic), API keys
  are stored in `~/.gleann/config.json`. Ensure restrictive file
  permissions (`chmod 600 ~/.gleann/config.json`).
- **REST server**: The `gleann serve` command binds to `localhost:8080`
  by default. There is **no built-in authentication** — if you expose the
  port to a network, place it behind an authenticated reverse proxy.
- **MCP server**: Runs over stdio (not network), so it is inherently
  local.
- **Plugin system**: Plugins run as local HTTP servers. **Only install
  plugins you trust** — they receive raw document text and may execute
  arbitrary code on your machine.

## Threat Model

| Threat | Mitigation |
|--------|------------|
| Prompt injection in indexed documents | Search results are returned as structured JSON; downstream LLM consumers (yaver, opencode, etc.) are responsible for treating retrieved content as untrusted. |
| Path traversal in `gleann build --docs` | The ingestion walker resolves symlinks and refuses to follow paths outside the chosen root. |
| Untrusted plugin execution | Plugins are launched as separate HTTP processes; the user explicitly enables each one in `~/.gleann/plugins.json`. |
| KuzuDB CGo memory safety | Pinned to upstream tagged release; the embedded DB only opens databases in `~/.gleann/indexes/`. |
| Vulnerable Go stdlib / 3rd-party | `govulncheck` is run on each release; latest report in [`security/govulncheck.txt`](security/govulncheck.txt). |
| Supply-chain compromise of deps | CycloneDX 1.5 SBOM committed in [`security/sbom.cdx.json`](security/sbom.cdx.json). |

## Supply Chain

- **SBOM**: A CycloneDX 1.5 SBOM is committed under
  [`security/sbom.cdx.json`](security/sbom.cdx.json) (~85 KB, ~100
  modules with license metadata). Regenerate with:
  ```bash
  cyclonedx-gomod mod -licenses -json -output security/sbom.cdx.json .
  ```
- **Vulnerability scan**: `govulncheck ./...` is run after every
  dependency bump. Result snapshot: [`security/govulncheck.txt`](security/govulncheck.txt).
- **Dependency policy**: Pin minor versions in `go.mod`; require a fix or
  documented mitigation for any non-test CVE that govulncheck reports as
  reachable from the call graph.
- **Go toolchain**: Track the latest patched 1.25.x release to inherit
  stdlib security fixes. Bump `go.mod`'s `go` directive whenever Google
  ships an `encoding/asn1` / `crypto/{tls,x509}` / `net/url` patch.

## Hardening Checklist (operators)

When deploying `gleann serve` in a shared environment:

- [ ] Bind to `127.0.0.1` only or front with a reverse proxy that
      authenticates.
- [ ] Set a per-host `GLEANN_INDEX_DIR` outside `~` to keep indexes
      isolated.
- [ ] Run as a non-root user; restrict the index directory with
      `umask 077`.
- [ ] Mount `/proc/self/maps` read-only inside containers (KuzuDB uses
      memory-mapped files).
- [ ] Keep `go-git`, `cloudflare/circl`, and other transitive
      cryptography deps current; they are commonly the source of CVEs.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest `main` | Yes — security fixes applied immediately |
| Latest tagged minor | Yes — backported patch fixes |
| < 1.0 | Best effort |
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
