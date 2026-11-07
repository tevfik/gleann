package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/pkg/gleann"
)

// StorageSwitcher converts an index between "store" and "recompute" vector
// storage in place (cmd/gleann setStorageMode: re-index from the stored
// passages, restore the old files on failure).
type StorageSwitcher func(cfg gleann.Config, name, target string) error

// SetStorageSwitcher enables POST /api/indexes/{name}/storage. cmd/gleann
// provides it, as for `gleann index storage` and the TUI Index Manager.
func (s *Server) SetStorageSwitcher(fn StorageSwitcher) { s.storageSwitcher = fn }

// storageInfo is the vector storage of one index as GET /api/indexes reports it.
type storageInfo struct {
	Mode        string `json:"mode"`
	IndexBytes  int64  `json:"index_bytes"`
	FullBytes   int64  `json:"full_bytes"`
	Convertible bool   `json:"convertible"`
	Note        string `json:"note,omitempty"`
}

func indexStorage(indexDir string, metas []gleann.IndexMeta) map[string]storageInfo {
	out := make(map[string]storageInfo, len(metas))
	for _, m := range metas {
		vs := gleann.InspectVectorStorage(indexDir, m)
		out[m.Name] = storageInfo{Mode: vs.Mode, IndexBytes: vs.IndexBytes, FullBytes: vs.FullBytes,
			Convertible: vs.Convertible, Note: vs.Note}
	}
	return out
}

// handleSetStorage serves POST /api/indexes/{name}/storage {"mode":"store"|"recompute"}:
// 202 {"task_id"} while the conversion runs in the background.
func (s *Server) handleSetStorage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.storageSwitcher == nil {
		writeError(w, http.StatusNotImplemented, "storage conversion is not available in this server")
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil ||
		(body.Mode != gleann.StorageStore && body.Mode != gleann.StorageRecompute) {
		writeError(w, http.StatusBadRequest, `body must be {"mode":"store"} or {"mode":"recompute"}`)
		return
	}
	cfg := s.cfg()
	meta, err := gleann.GetIndexMeta(cfg.IndexDir, name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	vs := gleann.InspectVectorStorage(cfg.IndexDir, *meta)
	if !vs.Convertible {
		writeError(w, http.StatusConflict, fmt.Sprintf("index %q cannot be switched: %s", name, vs.Note))
		return
	}
	if vs.Mode == body.Mode {
		writeJSON(w, http.StatusOK, map[string]any{"mode": vs.Mode, "unchanged": true})
		return
	}
	switcher := s.storageSwitcher
	taskID := s.bgManager.Submit(background.TaskTypeReIndex, func(progress func(float64, string)) error {
		progress(0, fmt.Sprintf("Switching %s to %s storage (re-indexing from stored passages)", name, body.Mode))
		s.invalidateSearcher(name) // release the open index files first
		err := switcher(cfg, name, body.Mode)
		s.invalidateSearcher(name)
		if err == nil {
			progress(1, fmt.Sprintf("%s now uses %s storage", name, body.Mode))
		}
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": taskID, "mode": body.Mode})
}
