package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestE2E_TokenBudgeting_CompleteAgentWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	indexName := "e2e_codebase"

	// 1. Prepare realistic codebase documents
	documents := []gleann.Item{
		{
			Text: "package auth\n\n// TokenManager validates and mints RS256 JWT tokens with custom claims.\ntype TokenManager struct {\n\tsecret []byte\n\tttl    int\n}\n\nfunc NewTokenManager(secret []byte) *TokenManager {\n\treturn &TokenManager{secret: secret, ttl: 3600}\n}",
			Metadata: map[string]any{
				"source": "pkg/auth/token.go",
				"kind":   "code",
				"line":   1,
			},
		},
		{
			Text: "package auth\n\n// Middleware enforces valid Bearer authorization header on incoming HTTP requests.\nfunc AuthMiddleware(tm *TokenManager) func(next Handler) Handler {\n\treturn func(next Handler) Handler {\n\t\t// Checks header and validates token validity\n\t\treturn next\n\t}\n}",
			Metadata: map[string]any{
				"source": "pkg/auth/middleware.go",
				"kind":   "code",
				"line":   1,
			},
		},
		{
			Text: strings.Repeat("# Architecture Decision Record: Zero-Trust Token Verification\nAll internal microservices must verify JWT signatures independently using locally cached public keys. ", 15),
			Metadata: map[string]any{
				"source": "docs/adr/001-tokens.md",
				"kind":   "docs",
				"line":   1,
			},
		},
		{
			Text: strings.Repeat("# Security Incident Response for Leaked Credentials\nWhen a secret or JWT signing key is suspected to be compromised, rotate certificates immediately and invalidate all active session tokens in BBolt cache. ", 15),
			Metadata: map[string]any{
				"source": "docs/security/incident_response.md",
				"kind":   "docs",
				"line":   1,
			},
		},
	}

	embedder := &testBudgetEmbedder{dim: 4}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = tmpDir
	glCfg.Backend = "hnsw"

	builder, err := gleann.NewBuilder(glCfg, embedder)
	if err != nil {
		t.Fatalf("Failed to create builder: %v", err)
	}

	ctx := context.Background()
	if err := builder.Build(ctx, indexName, documents); err != nil {
		t.Fatalf("Failed to build e2e index: %v", err)
	}

	// 2. Initialize MCP server and verify tools
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "custom",
		EmbeddingModel:    "test-model",
	})
	defer srv.Close()
	srv.embedder = embedder

	searcher := gleann.NewSearcher(glCfg, embedder)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("Failed to load searcher: %v", err)
	}
	srv.searcherMu.Lock()
	srv.searchers[indexName] = searcher
	srv.searcherMu.Unlock()

	// 3. Step A: Agent performs unconstrained search to evaluate baseline token usage
	unconstrainedReq := mcpsdk.CallToolRequest{}
	unconstrainedReq.Params.Arguments = map[string]interface{}{
		"index": indexName,
		"query": "JWT token verification and middleware",
		"top_k": float64(4),
	}
	resUnc, err := srv.handleSearch(ctx, unconstrainedReq)
	if err != nil || resUnc.IsError {
		t.Fatalf("Unconstrained search failed: err=%v, res=%v", err, resUnc)
	}
	uncText := extractText(resUnc)
	baselineTokens := gleann.EstimateTokens(uncText)
	t.Logf("Baseline unconstrained response tokens: %d", baselineTokens)

	// 4. Step B: Agent sets budget to 200 tokens
	mediumBudget := 200
	medReq := mcpsdk.CallToolRequest{}
	medReq.Params.Arguments = map[string]interface{}{
		"index":      indexName,
		"query":      "JWT token verification and middleware",
		"top_k":      float64(4),
		"max_tokens": float64(mediumBudget),
	}
	resMed, err := srv.handleSearch(ctx, medReq)
	if err != nil || resMed.IsError {
		t.Fatalf("Medium budget search failed: err=%v, res=%v", err, resMed)
	}
	medText := extractText(resMed)
	medTokens := gleann.EstimateTokens(medText)
	t.Logf("Medium budget (max 200) response tokens: %d", medTokens)

	if medTokens > mediumBudget {
		t.Fatalf("Invariant broken! medTokens=%d > mediumBudget=%d", medTokens, mediumBudget)
	}
	if !strings.Contains(medText, "[Token Budget:") {
		t.Errorf("expected [Token Budget: summary note in output")
	}

	// 5. Step C: Agent sets a very tight budget of 70 tokens
	tightBudget := 70
	tightReq := mcpsdk.CallToolRequest{}
	tightReq.Params.Arguments = map[string]interface{}{
		"index":      indexName,
		"query":      "JWT token verification and middleware",
		"top_k":      float64(4),
		"max_tokens": float64(tightBudget),
	}
	resTight, err := srv.handleSearch(ctx, tightReq)
	if err != nil || resTight.IsError {
		t.Fatalf("Tight budget search failed: err=%v, res=%v", err, resTight)
	}
	tightText := extractText(resTight)
	tightTokens := gleann.EstimateTokens(tightText)
	t.Logf("Tight budget (max 70) response tokens: %d", tightTokens)

	if tightTokens > tightBudget {
		t.Fatalf("Invariant broken! tightTokens=%d > tightBudget=%d\nOutput:\n%s", tightTokens, tightBudget, tightText)
	}

	// In tight budget, at least one result must have degraded to locator or dropped
	if !strings.Contains(tightText, "[Locator only]") && !strings.Contains(tightText, "dropped") {
		t.Errorf("expected locator degradation or drops under tight budget, got:\n%s", tightText)
	}

	// Verify that tight tokens < medium tokens < baseline tokens
	if tightTokens >= medTokens || medTokens >= baselineTokens {
		t.Errorf("Expected strict hierarchy tight (%d) < med (%d) < baseline (%d)",
			tightTokens, medTokens, baselineTokens)
	}
}
