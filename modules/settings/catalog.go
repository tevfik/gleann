package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Model is one downloadable item of a Catalog: a model file, a bundle of
// files or a runtime library.
type Model struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group,omitempty"`
	Size  string `json:"size,omitempty"` // human readable, e.g. "466 MB"
	Note  string `json:"note,omitempty"`
	// Value is what a setting stores to use this model (a path or file
	// name), so a client can offer "use it" next to "download it".
	Value string `json:"value,omitempty"`
	// Installed reports that the model is on disk and usable.
	Installed bool `json:"installed"`
	// Blocked, when not empty, says why it cannot be downloaded yet (for
	// example a license that must be accepted in the settings first).
	Blocked string `json:"blocked,omitempty"`
}

// Progress is a download's state.
type Progress struct {
	Done    int64  `json:"done,omitempty"`  // bytes so far
	Total   int64  `json:"total,omitempty"` // bytes expected; 0 = unknown
	Message string `json:"message,omitempty"`
}

// Catalog lists what an application can download and downloads it. The
// application keeps its own catalog (URLs, checksums, file layout); Catalog
// only exposes it under one contract, so the gleann web UI offers downloads
// for every plugin the same way.
type Catalog struct {
	// List returns the models with their current Installed state.
	List func() []Model
	// Download fetches one model, reporting progress. It runs in the
	// background, not bound to the request that started it.
	Download func(ctx context.Context, id string, report func(Progress)) error
}

// task is a running or finished download.
type task struct {
	State string `json:"state"` // downloading | failed
	Progress
	Error string `json:"error,omitempty"`
}

// ModelState is a Model with the state of its download, as GET returns it.
type ModelState struct {
	Model
	State    string   `json:"state"` // installed | available | downloading | failed | blocked
	Progress Progress `json:"progress,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Handler serves the catalog contract:
//
//	GET  /models              -> {"models":[ModelState...]} in List order
//	POST /models {"id":"..."} -> 202 {"id":...,"state":"downloading"}
//	                             404 unknown id, 409 already downloading or blocked
//
// Downloads run with ctx (the server's lifetime), one per id at a time. A
// failed download keeps its error in the list until it is retried.
func (cat *Catalog) Handler(ctx context.Context, authorized func(*http.Request) bool) http.Handler {
	var mu sync.Mutex
	tasks := map[string]*task{}

	find := func(id string) (Model, bool) {
		for _, m := range cat.List() {
			if m.ID == id {
				return m, true
			}
		}
		return Model{}, false
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if authorized != nil && !authorized(req) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not authorized"})
			return
		}
		switch req.Method {
		case http.MethodGet:
			models := cat.List()
			out := make([]ModelState, 0, len(models))
			mu.Lock()
			for _, m := range models {
				s := ModelState{Model: m, State: "available"}
				switch t := tasks[m.ID]; {
				case t != nil && t.State == "downloading":
					s.State, s.Progress = "downloading", t.Progress
				case m.Installed:
					s.State = "installed"
				case t != nil && t.State == "failed":
					s.State, s.Error = "failed", t.Error
				case m.Blocked != "":
					s.State = "blocked"
				}
				out = append(out, s)
			}
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"models": out})

		case http.MethodPost:
			if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"})
				return
			}
			var body struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<16)).Decode(&body); err != nil || body.ID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": `body must be {"id":"..."}`})
				return
			}
			m, ok := find(body.ID)
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown model " + body.ID})
				return
			}
			if m.Blocked != "" {
				writeJSON(w, http.StatusConflict, map[string]any{"error": m.Blocked})
				return
			}
			mu.Lock()
			if t := tasks[m.ID]; t != nil && t.State == "downloading" {
				mu.Unlock()
				writeJSON(w, http.StatusConflict, map[string]any{"error": m.Name + " is already downloading"})
				return
			}
			t := &task{State: "downloading", Progress: Progress{Message: "starting"}}
			tasks[m.ID] = t
			mu.Unlock()

			go func() {
				err := cat.Download(ctx, m.ID, func(p Progress) {
					mu.Lock()
					t.Progress = p
					mu.Unlock()
				})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.State, t.Error = "failed", err.Error()
					return
				}
				delete(tasks, m.ID) // List reports it installed now
			}()
			writeJSON(w, http.StatusAccepted, map[string]any{"id": m.ID, "state": "downloading"})

		default:
			w.Header().Set("Allow", "GET, POST")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "only GET and POST are supported"})
		}
	})
}

// HumanSize formats bytes as "466 MB".
func HumanSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%d MB", b>>20)
	case b >= 1<<10:
		return fmt.Sprintf("%d KB", b>>10)
	}
	return fmt.Sprintf("%d B", b)
}
