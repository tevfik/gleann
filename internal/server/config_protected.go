package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// httpProtectedKeys are the settings an API client may not change. They decide
// what the server protects (the directories it may read, where indexes live,
// whether a token is needed, which web pages may call it), so an admin token
// does not stand in for the OS user who owns the config file: change them
// with `gleann setup`, the TUI settings screen or by editing config.json, then
// restart the server.
var httpProtectedKeys = []string{"allowed_roots", "index_dir", "auth", "cors_origins"}

// changedHTTPProtected lists the protected keys whose value differs between
// prev and next (a round-tripped, unchanged value is accepted, so a client
// that posts back the whole GET /api/config answer still works).
func changedHTTPProtected(prev, next gleann.Config) []string {
	var changed []string
	if !slices.Equal(prev.AllowedRoots, next.AllowedRoots) {
		changed = append(changed, "allowed_roots")
	}
	if prev.IndexDir != next.IndexDir {
		changed = append(changed, "index_dir")
	}
	if prev.Auth != next.Auth {
		changed = append(changed, "auth")
	}
	if !slices.Equal(prev.CORSOrigins, next.CORSOrigins) {
		changed = append(changed, "cors_origins")
	}
	return changed
}

func httpProtectedMessage(keys []string) string {
	return fmt.Sprintf("%s cannot be changed through the API; edit config.json or use `gleann setup`, then restart the server", strings.Join(keys, ", "))
}

// guardSettingsPut refuses a PUT /api/settings that sets a protected key. The
// form sends only the fields the user changed, so presence is a change.
func guardSettingsPut(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if len(bytes.TrimSpace(body)) == 0 {
			next.ServeHTTP(w, r) // the handler answers an empty body itself
			return
		}
		// Decode as the registry handler does (first JSON value wins, so
		// trailing data would let a guard that parses the whole body pass a
		// request the handler still reads); anything but one clean JSON value
		// is a 400.
		dec := json.NewDecoder(bytes.NewReader(body))
		var req struct {
			Values map[string]json.RawMessage `json:"values"`
		}
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, `body must be {"values":{...}}`)
			return
		}
		if _, err := dec.Token(); err != io.EOF {
			writeError(w, http.StatusBadRequest, "unexpected data after the JSON body")
			return
		}
		var set []string
		for _, k := range httpProtectedKeys {
			if _, ok := req.Values[k]; ok {
				set = append(set, k)
			}
		}
		if len(set) > 0 {
			writeError(w, http.StatusForbidden, httpProtectedMessage(set))
			return
		}
		next.ServeHTTP(w, r)
	})
}
