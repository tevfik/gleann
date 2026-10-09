package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestIntegration_DefTool_Schema(t *testing.T) {
	srv := NewServer(Config{IndexDir: t.TempDir()})

	defTool := srv.buildDefTool()
	if defTool.Name != "gleann_def" {
		t.Fatalf("expected tool name gleann_def, got: %s", defTool.Name)
	}

	props := defTool.InputSchema.Properties
	for _, expectedProp := range []string{"symbol", "index", "kind", "top_k"} {
		if _, exists := props[expectedProp]; !exists {
			t.Errorf("gleann_def schema missing property %q", expectedProp)
		}
	}

	if len(defTool.InputSchema.Required) == 0 || defTool.InputSchema.Required[0] != "symbol" {
		t.Errorf("expected required field ['symbol'], got: %v", defTool.InputSchema.Required)
	}
}

func setupDefTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	tmpDir := t.TempDir()
	indexName := "def_test_index"

	passages := []gleann.Item{
		{
			Text: "// TokenManager coordinates JWT minting and revocation.\ntype TokenManager struct {\n\tsecret []byte\n}\n\n// NewTokenManager creates a new token manager instance.\nfunc NewTokenManager(secret []byte) *TokenManager {\n\treturn &TokenManager{secret: secret}\n}",
			Metadata: map[string]any{
				"source": "auth/manager.go",
				"kind":   "code",
				"line":   2,
			},
		},
		{
			Text: "// VerifyToken checks token validity.\nfunc VerifyToken(token string) bool {\n\treturn len(token) > 0\n}",
			Metadata: map[string]any{
				"source": "auth/verify.go",
				"kind":   "code",
				"line":   2,
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

func TestIntegration_HandleDef_SuccessAndFiltering(t *testing.T) {
	srv, indexName := setupDefTestServer(t)
	defer srv.Close()

	ctx := context.Background()

	// 1. Missing symbol argument returns error
	reqEmpty := mcpsdk.CallToolRequest{}
	reqEmpty.Params.Arguments = map[string]interface{}{}
	resEmpty, _ := srv.handleDef(ctx, reqEmpty)
	if !resEmpty.IsError {
		t.Error("expected error for empty symbol argument")
	}

	// 2. Lookup definition for TokenManager (struct)
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"index":  indexName,
		"symbol": "TokenManager",
	}
	res, err := srv.handleDef(ctx, req)
	if err != nil || res.IsError {
		t.Fatalf("handleDef failed: err=%v, res=%v", err, res)
	}

	text := extractText(res)
	if !strings.Contains(text, "auth/manager.go") {
		t.Errorf("expected definition to point to auth/manager.go, got:\n%s", text)
	}
	if !strings.Contains(text, "Defined at:") {
		t.Errorf("expected 'Defined at:' location line, got:\n%s", text)
	}
	if !strings.Contains(text, "type TokenManager struct") {
		t.Errorf("expected signature for TokenManager, got:\n%s", text)
	}

	// 3. Lookup definition for VerifyToken (function)
	reqFunc := mcpsdk.CallToolRequest{}
	reqFunc.Params.Arguments = map[string]interface{}{
		"index":  indexName,
		"symbol": "VerifyToken",
		"kind":   "function",
	}
	resFunc, err := srv.handleDef(ctx, reqFunc)
	if err != nil || resFunc.IsError {
		t.Fatalf("handleDef VerifyToken failed: %v", err)
	}
	textFunc := extractText(resFunc)
	if !strings.Contains(textFunc, "func VerifyToken") {
		t.Errorf("expected func VerifyToken signature, got:\n%s", textFunc)
	}

	// 3. Lookup non-existent symbol
	reqNone := mcpsdk.CallToolRequest{}
	reqNone.Params.Arguments = map[string]interface{}{
		"index":  indexName,
		"symbol": "NonExistentSymbolXYZ",
	}
	resNone, err := srv.handleDef(ctx, reqNone)
	if err != nil {
		t.Fatal(err)
	}
	textNone := extractText(resNone)
	if !strings.Contains(textNone, "No definition found for symbol") {
		t.Errorf("expected friendly not found message, got:\n%s", textNone)
	}
}
