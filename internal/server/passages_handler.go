// Package server — read-only listing of the stored passages and sections of
// an index (RFC 004 G8).
//
//	GET /api/indexes/{name}/passages?limit=N&offset=M
//	GET /api/indexes/{name}/sections?limit=N&offset=M
//
// Both routes read the index's SQLite record store (internal/store) directly:
// they do not acquire a searcher, so no vector file, embedder or reranker is
// loaded just to list metadata. They are access-checked like the other index
// routes (ValidateName, checkIndexAccess) and need the query token scope,
// because requiredScope classifies every GET under /api/indexes as ScopeQuery.
package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/tevfik/gleann/pkg/gleann"
)

// indexPassageListResponse is the body of GET /api/indexes/{name}/passages.
// Limit and Offset echo the page that was served (Limit is the effective page
// size, i.e. the default when the request had no ?limit=).
type indexPassageListResponse struct {
	Passages []gleann.IndexPassage `json:"passages"`
	Count    int                   `json:"count"`
	Limit    int                   `json:"limit"`
	Offset   int                   `json:"offset"`
}

// indexSectionListResponse is the body of GET /api/indexes/{name}/sections.
type indexSectionListResponse struct {
	Sections []gleann.IndexSection `json:"sections"`
	Count    int                   `json:"count"`
	Limit    int                   `json:"limit"`
	Offset   int                   `json:"offset"`
}

// handleListPassages handles GET /api/indexes/{name}/passages.
func (s *Server) handleListPassages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}
	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	limit, offset, err := parseListingPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	passages, err := gleann.ListStoredPassages(r.Context(), s.cfg().IndexDir, name, limit, offset)
	if err != nil {
		writeListingError(w, name, err)
		return
	}
	if passages == nil {
		passages = []gleann.IndexPassage{}
	}
	writeJSON(w, http.StatusOK, indexPassageListResponse{
		Passages: passages,
		Count:    len(passages),
		Limit:    effectiveListLimit(limit),
		Offset:   offset,
	})
}

// handleListSections handles GET /api/indexes/{name}/sections.
func (s *Server) handleListSections(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}
	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	limit, offset, err := parseListingPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sections, err := gleann.ListStoredSections(r.Context(), s.cfg().IndexDir, name, limit, offset)
	if err != nil {
		writeListingError(w, name, err)
		return
	}
	if sections == nil {
		sections = []gleann.IndexSection{}
	}
	writeJSON(w, http.StatusOK, indexSectionListResponse{
		Sections: sections,
		Count:    len(sections),
		Limit:    effectiveListLimit(limit),
		Offset:   offset,
	})
}

// effectiveListLimit is the page size a request is served with: the store
// default when the request carried no ?limit= (limit 0).
func effectiveListLimit(limit int) int {
	if limit <= 0 {
		return gleann.DefaultPassageListLimit
	}
	return limit
}

// parseListingPage reads ?limit= and ?offset=. An absent parameter means the
// default (limit) or 0 (offset); a present but non-numeric, empty,
// non-positive limit, negative offset or a limit above
// gleann.MaxPassageListLimit is an error the handler answers with 400, never
// a silent clamp.
func parseListingPage(r *http.Request) (limit, offset int, err error) {
	q := r.URL.Query()
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > gleann.MaxPassageListLimit {
			return 0, 0, fmt.Errorf("limit must be an integer between 1 and %d", gleann.MaxPassageListLimit)
		}
	}
	if q.Has("offset") {
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

// writeListingError maps a record-store error to its HTTP status: an old
// store format is the migration refusal (409, as on the other index routes),
// a missing index is 404, anything else (a damaged or unreadable database) is
// a server fault.
func writeListingError(w http.ResponseWriter, name string, err error) {
	switch {
	case errors.Is(err, gleann.ErrOldStoreFormat):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, gleann.ErrIndexNotFound):
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found", name))
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
