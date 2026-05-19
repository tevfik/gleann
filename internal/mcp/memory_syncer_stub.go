//go:build !treesitter

package mcp

import (
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// wireMemorySyncer is a no-op when built without treesitter support.
// The Memory Engine is not available without KuzuDB (which requires treesitter tag).
func (srv *Server) wireMemorySyncer(_ Config, _ gleann.Config, _ *embedding.Computer) {}
