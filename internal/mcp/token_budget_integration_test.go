package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)


type testBudgetEmbedder struct {
	dim int
}

func (e *testBudgetEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, e.dim)
		for j := range vec {
			vec[j] = float32(len(text)+j) * 0.01
		}
		result[i] = vec
	}
	return result, nil
}

func (e *testBudgetEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, _ := e.Compute(ctx, []string{text})
	return v[0], nil
}

func (e *testBudgetEmbedder) Dimensions() int   { return e.dim }
func (e *testBudgetEmbedder) ModelName() string { return "test-model" }

func setupBudgetTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	tmpDir := t.TempDir()
	indexName := "budget_test_index"

	passages := []gleann.Item{
		{
			Text: "Authentication service handles JWT verification and OAuth2 token exchanges for all incoming API requests.",
			Metadata: map[string]any{
				"source": "auth/service.go",
				"kind":   "code",
			},
		},
		{
			Text: strings.Repeat("Detailed role-based access control (RBAC) rules and permission matrix for administrators and users. ", 10),
			Metadata: map[string]any{
				"source": "auth/rbac.go",
				"kind":   "code",
			},
		},
		{
			Text: strings.Repeat("Audit logging mechanism records every successful and failed login attempt into secure persistent storage. ", 10),
			Metadata: map[string]any{
				"source": "auth/audit.go",
				"kind":   "code",
			},
		},
		{
			Text: strings.Repeat("Session revocation endpoint invalidates active refresh tokens across all clustered instances immediately. ", 10),
			Metadata: map[string]any{
				"source": "auth/session.go",
				"kind":   "code",
			},
		},
	}

	embedder := &testBudgetEmbedder{dim: 4}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = tmpDir
	glCfg.Backend = "hnsw"

	builder, err := gleann.NewBuilder(glCfg, embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := builder.Build(ctx, indexName, passages); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "custom",
		EmbeddingModel:    "test-model",
	}
	srv := NewServer(cfg)
	srv.embedder = embedder

	searcher := gleann.NewSearcher(glCfg, embedder)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatal(err)
	}

	srv.searcherMu.Lock()
	srv.searchers[indexName] = searcher
	srv.searcherMu.Unlock()

	return srv, indexName
}

func TestIntegration_ToolSchemaHasMaxTokens(t *testing.T) {
	srv := NewServer(Config{IndexDir: t.TempDir()})

	// 1. gleann_search
	searchTool := srv.buildSearchTool()
	props := searchTool.InputSchema.Properties
	if _, exists := props["max_tokens"]; !exists {
		t.Error("gleann_search schema is missing max_tokens property")
	}

	// 2. gleann_search_multi
	multiTool := srv.buildSearchMultiTool()
	multiProps := multiTool.InputSchema.Properties
	if _, exists := multiProps["max_tokens"]; !exists {
		t.Error("gleann_search_multi schema is missing max_tokens property")
	}
}

func TestIntegration_HandleSearch_TokenBudgetEnforcement(t *testing.T) {
	srv, indexName := setupBudgetTestServer(t)
	defer srv.Close()

	ctx := context.Background()

	// 1. Unconstrained search (max_tokens omitted)
	unconstrainedReq := mcpsdk.CallToolRequest{}
	unconstrainedReq.Params.Arguments = map[string]interface{}{
		"index": indexName,
		"query": "authentication tokens",
		"top_k": float64(4),
	}
	resUnc, err := srv.handleSearch(ctx, unconstrainedReq)
	if err != nil {
		t.Fatal(err)
	}
	if resUnc.IsError {
		t.Fatalf("unexpected error result: %v", resUnc)
	}
	uncText := extractText(resUnc)
	uncTokens := gleann.EstimateTokens(uncText)

	// Must not have token budget note when unconstrained
	if strings.Contains(uncText, "[Token Budget:") {
		t.Errorf("unconstrained search should not include [Token Budget: note, got:\n%s", uncText)
	}

	// 2. Budgeted search with max_tokens = 150
	maxTokens := 150
	budgetedReq := mcpsdk.CallToolRequest{}
	budgetedReq.Params.Arguments = map[string]interface{}{
		"index":      indexName,
		"query":      "authentication tokens",
		"top_k":      float64(4),
		"max_tokens": float64(maxTokens),
	}
	resBud, err := srv.handleSearch(ctx, budgetedReq)
	if err != nil {
		t.Fatal(err)
	}
	if resBud.IsError {
		t.Fatalf("unexpected error result: %v", resBud)
	}
	budText := extractText(resBud)
	budTokens := gleann.EstimateTokens(budText)

	// Strict invariant check: budTokens <= maxTokens
	if budTokens > maxTokens {
		t.Fatalf("Strict budget invariant violated! budTokens=%d > maxTokens=%d\nOutput:\n%s", budTokens, maxTokens, budText)
	}

	// Must have token budget accounting note
	if !strings.Contains(budText, "[Token Budget:") {
		t.Errorf("budgeted search must include [Token Budget: summary note, got:\n%s", budText)
	}

	// Must contain locator degradation
	if !strings.Contains(budText, "[Locator only]") {
		t.Errorf("expected at least one degraded locator result, got:\n%s", budText)
	}

	// Budgeted search must consume fewer tokens than unconstrained search
	if budTokens >= uncTokens {
		t.Errorf("expected budTokens (%d) < uncTokens (%d)", budTokens, uncTokens)
	}
}

func TestIntegration_HandleSearch_TightBudget(t *testing.T) {
	srv, indexName := setupBudgetTestServer(t)
	defer srv.Close()

	ctx := context.Background()

	// Extremely tight budget: 45 tokens
	maxTokens := 45
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"index":      indexName,
		"query":      "authentication tokens",
		"top_k":      float64(4),
		"max_tokens": float64(maxTokens),
	}
	res, err := srv.handleSearch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res)
	}
	text := extractText(res)
	usedTokens := gleann.EstimateTokens(text)

	if usedTokens > maxTokens {
		t.Fatalf("Invariant violated! usedTokens=%d > maxTokens=%d\nOutput:\n%s", usedTokens, maxTokens, text)
	}

	if !strings.Contains(text, "[Token Budget:") {
		t.Errorf("expected summary note in output:\n%s", text)
	}
}

func TestIntegration_HandleSearchMulti_TokenBudget(t *testing.T) {
	srv, indexName := setupBudgetTestServer(t)
	defer srv.Close()

	ctx := context.Background()

	// 1. Unconstrained multi-search
	reqUnc := mcpsdk.CallToolRequest{}
	reqUnc.Params.Arguments = map[string]interface{}{
		"query":   "authentication tokens",
		"indexes": indexName,
		"top_k":   float64(4),
	}
	resUnc, err := srv.handleSearchMulti(ctx, reqUnc)
	if err != nil {
		t.Fatal(err)
	}
	uncText := extractText(resUnc)
	if strings.Contains(uncText, "[Token Budget:") {
		t.Errorf("unconstrained multi search should not have token budget note, got:\n%s", uncText)
	}

	// 2. Budgeted multi-search
	maxTokens := 120
	reqBud := mcpsdk.CallToolRequest{}
	reqBud.Params.Arguments = map[string]interface{}{
		"query":      "authentication tokens",
		"indexes":    indexName,
		"top_k":      float64(4),
		"max_tokens": float64(maxTokens),
	}
	resBud, err := srv.handleSearchMulti(ctx, reqBud)
	if err != nil {
		t.Fatal(err)
	}
	budText := extractText(resBud)
	budTokens := gleann.EstimateTokens(budText)

	if budTokens > maxTokens {
		t.Fatalf("Invariant violated! budTokens=%d > maxTokens=%d\nOutput:\n%s", budTokens, maxTokens, budText)
	}

	if !strings.Contains(budText, "[Token Budget:") {
		t.Errorf("expected summary note in multi-search output, got:\n%s", budText)
	}
}

