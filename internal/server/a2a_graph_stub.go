//go:build !treesitter

package server

import (
	"fmt"

	"github.com/tevfik/gleann/internal/a2a"
)

func (s *Server) a2aCommunitiesHandler(ctx a2a.SkillContext) (string, error) {
	return "", fmt.Errorf("community detection requires treesitter build tag")
}

func (s *Server) a2aRepoMapHandler(ctx a2a.SkillContext) (string, error) {
	return "", fmt.Errorf("repo map requires treesitter build tag")
}

func (s *Server) a2aRiskHandler(ctx a2a.SkillContext) (string, error) {
	return "", fmt.Errorf("risk analysis requires treesitter build tag")
}
