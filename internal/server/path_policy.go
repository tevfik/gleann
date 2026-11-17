package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/pathpolicy"
)

// allowedRoots returns the directories a client may ask the server to index or
// read: GLEANN_ALLOWED_ROOTS (path-list separated), else allowed_roots from the
// config, else the user's home directory.
func (s *Server) allowedRoots() []string {
	if env := strings.TrimSpace(os.Getenv("GLEANN_ALLOWED_ROOTS")); env != "" {
		return pathpolicy.DefaultRoots(filepath.SplitList(env))
	}
	return pathpolicy.DefaultRoots(s.cfg().AllowedRoots)
}

// containClientPath resolves a client-supplied path and checks it lies inside
// the allowed roots. On refusal it writes a 403 naming the setting and
// returns false.
func (s *Server) containClientPath(w http.ResponseWriter, p string) (string, bool) {
	resolved, err := pathpolicy.Contain(p, s.allowedRoots())
	if err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf("path %q is not inside the directories this server may read; add its parent to allowed_roots in config.json (or GLEANN_ALLOWED_ROOTS)", p))
		return "", false
	}
	return resolved, true
}
