//go:build !treesitter

package server

import (
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// initMemorySyncer is a no-op in !treesitter builds.
func (s *Server) initMemorySyncer(_ gleann.Config, _ *embedding.Computer) {}
