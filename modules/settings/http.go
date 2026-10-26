package settings

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Handler serves the settings contract:
//
//	GET /settings -> Form
//	PUT /settings {"values":{key:value,...},"revision":"..."}
//	              -> {"saved":[keys],"restart_required":bool,"revision":"..."}
//	              or 422 {"error":...,"errors":{key:message}} with nothing saved
//	              or 409 {"error":...,"revision":"..."} when revision is given
//	                 and the config changed since that GET
//
// authorized, when not nil, gates both methods. PUT must be sent as
// application/json, which makes browsers preflight cross-site requests.
func (r *Registry[C]) Handler(authorized func(*http.Request) bool) http.Handler {
	var mu sync.Mutex // one load-modify-save at a time
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if authorized != nil && !authorized(req) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not authorized"})
			return
		}
		switch req.Method {
		case http.MethodGet:
			c, err := r.Load()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "loading settings: " + err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, r.Describe(c))
		case http.MethodPut:
			mu.Lock()
			defer mu.Unlock()
			r.put(w, req)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "only GET and PUT are supported"})
		}
	})
}

func (r *Registry[C]) put(w http.ResponseWriter, req *http.Request) {
	if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "Content-Type must be application/json"})
		return
	}
	var body struct {
		Values   map[string]any `json:"values"`
		Revision string         `json:"revision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil || len(body.Values) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `body must be {"values":{...}} with at least one value`})
		return
	}
	c, err := r.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "loading settings: " + err.Error()})
		return
	}
	if body.Revision != "" && body.Revision != Revision(c) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    "the settings were changed elsewhere since this form was loaded; reload and try again",
			"revision": Revision(c),
		})
		return
	}
	changed, errs := r.Update(c, body.Values)
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid settings; nothing was saved", "errors": errs})
		return
	}
	if r.BeforeSave != nil {
		r.BeforeSave(c)
	}
	if err := r.Save(c); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "saving: " + err.Error()})
		return
	}
	// The revision of what a GET now returns, which may differ from c when
	// Load normalises what it reads.
	if saved, err := r.Load(); err == nil {
		c = saved
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved":            changed,
		"restart_required": r.NeedsRestart(changed),
		"revision":         Revision(c),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
