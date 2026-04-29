// Mount the Knowledge Packs registry under /api/packs.
//
// PacksDir resolution order:
//  1. GLEANN_PACKS_DIR environment variable (absolute or relative).
//  2. <IndexDir>/packs (default).
//
// Failures are logged but never block server startup — packs are an optional
// surface; running gleann without any packs is perfectly valid.
package server

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann/internal/packs"
	"github.com/tevfik/gleann/internal/packs/packshttp"
)

func (s *Server) mountPacks(mux *http.ServeMux) {
	dir := os.Getenv("GLEANN_PACKS_DIR")
	if dir == "" {
		dir = filepath.Join(s.config.IndexDir, "packs")
	}
	reg := packs.New(dir)
	if err := reg.Reload(); err != nil {
		log.Printf("packs: reload warnings (server starts anyway): %v", err)
	}
	if n := len(reg.List()); n > 0 {
		log.Printf("packs: loaded %d pack(s) from %s", n, dir)
	}
	packshttp.New(reg).Mount(mux)
}
