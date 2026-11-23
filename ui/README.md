# gleann web UI

The browser interface served by `gleann serve`: chat, index list, code and knowledge graph views, memory, model catalog, plugin and general settings. React, TypeScript and Vite.

**Status: experimental.** The CLI and the TUI are gleann's main interfaces; this UI covers a subset of them and may change between minor versions.

## Development

```bash
cd ui
npm install
npm run dev      # Vite dev server
npm run test     # unit tests (node --test)
npm run lint     # oxlint
npm run build    # writes ui/dist
```

`make build` builds the UI when `ui/dist` is missing and copies it to `internal/server/dist`, where the Go server embeds it (`//go:embed dist/*`). `SKIP_WEB=1 make build` embeds a placeholder page instead (API-only build).
