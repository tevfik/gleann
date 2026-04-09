//go:build !treesitter

package server

import "net/http"

// recallGraphImpl is a no-op stub when KuzuDB is not available.
func (s *Server) recallGraphImpl() *RecallGraph {
	return nil
}

// injectGraphRelationship is a no-op stub when KuzuDB is not available.
func (s *Server) injectGraphRelationship(_ *http.Request, _ string, _ IngestRelationship) error {
	return nil
}
