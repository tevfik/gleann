// Package packshttp wires the [packs.Registry] into a stdlib http.ServeMux
// using gleann's existing routing convention.
//
// Routes registered:
//
//	GET /api/packs                          → list manifests
//	GET /api/packs/{id}                     → single manifest
//	GET /api/packs/{id}/data                → full pack (manifest + items)
//	GET /api/packs/{id}/items/{slug}        → single item
//	GET /api/packs/{id}/search?q=...&n=...  → substring search
//	POST /api/packs/reload                  → re-scan packs directory (admin)
//
// All endpoints honour `If-None-Match` and emit ETag/Cache-Control headers
// so downstream proxies (datum-server) can cache aggressively.
package packshttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/tevfik/gleann/internal/packs"
)

// Handler holds a registry plus configuration.
type Handler struct {
	Registry *packs.Registry
}

// New builds a handler bound to the given registry.
func New(r *packs.Registry) *Handler { return &Handler{Registry: r} }

// Mount registers all routes on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/packs", h.list)
	mux.HandleFunc("GET /api/packs/{id}", h.manifest)
	mux.HandleFunc("GET /api/packs/{id}/data", h.data)
	mux.HandleFunc("GET /api/packs/{id}/items/{slug}", h.item)
	mux.HandleFunc("GET /api/packs/{id}/search", h.search)
	mux.HandleFunc("POST /api/packs/reload", h.reload)
}

// ── handlers ───────────────────────────────────────────────────────────────

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	all := h.Registry.List()
	out := make([]packs.Manifest, 0, len(all))
	app := r.URL.Query().Get("app")
	for _, p := range all {
		if app != "" && !manifestMatchesApp(p.Manifest, app) {
			continue
		}
		out = append(out, p.Manifest)
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": out, "count": len(out)})
}

func (h *Handler) manifest(w http.ResponseWriter, r *http.Request) {
	p, err := h.Registry.Get(r.PathValue("id"))
	if respondNotFound(w, err) {
		return
	}
	if !setCacheHeaders(w, r, p.ETag) {
		return
	}
	writeJSON(w, http.StatusOK, p.Manifest)
}

func (h *Handler) data(w http.ResponseWriter, r *http.Request) {
	p, err := h.Registry.Get(r.PathValue("id"))
	if respondNotFound(w, err) {
		return
	}
	if !setCacheHeaders(w, r, p.ETag) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manifest": p.Manifest,
		"items":    p.Items,
	})
}

func (h *Handler) item(w http.ResponseWriter, r *http.Request) {
	it, err := h.Registry.Item(r.PathValue("id"), r.PathValue("slug"))
	if respondNotFound(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := 50
	if n := r.URL.Query().Get("n"); n != "" {
		if v, err := strconv.Atoi(n); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}
	items, err := h.Registry.Search(r.PathValue("id"), q, limit)
	if respondNotFound(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
		"query": q,
	})
}

func (h *Handler) reload(w http.ResponseWriter, _ *http.Request) {
	if err := h.Registry.Reload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reloaded": true,
		"count":    len(h.Registry.List()),
	})
}

// ── helpers ────────────────────────────────────────────────────────────────

func manifestMatchesApp(m packs.Manifest, app string) bool {
	for _, h := range m.AppHints {
		if h.AppID == app {
			return true
		}
	}
	return false
}

func respondNotFound(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, packs.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return true
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	return true
}

func setCacheHeaders(w http.ResponseWriter, r *http.Request, etag string) bool {
	if etag != "" {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=300")
		if match := r.Header.Get("If-None-Match"); match == etag {
			w.WriteHeader(http.StatusNotModified)
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
