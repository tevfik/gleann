package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

type testDefEmbedder struct {
	dim int
}

func (e *testDefEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
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

func (e *testDefEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, _ := e.Compute(ctx, []string{text})
	return v[0], nil
}

func (e *testDefEmbedder) Dimensions() int   { return e.dim }
func (e *testDefEmbedder) ModelName() string { return "test-model" }

func captureOutput(f func()) string {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestE2E_CmdDef(t *testing.T) {
	tmpDir := t.TempDir()
	indexName := "cmd_def_e2e_index"

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

	embedder := &testDefEmbedder{dim: 4}
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

	// Override config dir for test
	t.Setenv("GLEANN_INDEX_DIR", tmpDir)

	// 1. Test CLI with --json output
	outputJSON := captureOutput(func() {
		cmdDef([]string{"TokenManager", "--index", indexName, "--index-dir", tmpDir, "--json"})
	})

	var defs []gleann.DefinitionResult
	if err := json.Unmarshal([]byte(outputJSON), &defs); err != nil {
		t.Fatalf("Failed to parse JSON output: %v\nOutput:\n%s", err, outputJSON)
	}

	if len(defs) == 0 {
		t.Fatalf("Expected at least 1 definition, got 0. Output: %s", outputJSON)
	}

	if defs[0].Name != "TokenManager" {
		t.Errorf("Expected Name TokenManager, got %s", defs[0].Name)
	}
	if defs[0].Kind != "struct" {
		t.Errorf("Expected Kind struct, got %s", defs[0].Kind)
	}
	if defs[0].File != "auth/manager.go" {
		t.Errorf("Expected File auth/manager.go, got %s", defs[0].File)
	}

	// 2. Test CLI with formatted human output
	outputFormatted := captureOutput(func() {
		cmdDef([]string{"VerifyToken", "--index", indexName, "--index-dir", tmpDir, "--kind", "function"})
	})

	if !strings.Contains(outputFormatted, "Symbol: VerifyToken") {
		t.Errorf("Expected output to contain Symbol: VerifyToken, got:\n%s", outputFormatted)
	}
	if !strings.Contains(outputFormatted, "auth/verify.go") {
		t.Errorf("Expected output to contain auth/verify.go, got:\n%s", outputFormatted)
	}
	if !strings.Contains(outputFormatted, "func VerifyToken") {
		t.Errorf("Expected output to contain func VerifyToken, got:\n%s", outputFormatted)
	}

	// 3. Test non-existent symbol
	outputNone := captureOutput(func() {
		cmdDef([]string{"GhostSymbol", "--index", indexName, "--index-dir", tmpDir})
	})
	if !strings.Contains(outputNone, "No definition found for symbol") {
		t.Errorf("Expected not found message, got:\n%s", outputNone)
	}
}
