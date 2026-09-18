package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestMCPIndexGovernance(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 3 indexes:
	// 1. "public-work" (exposed=true, tag="work")
	// 2. "private-work" (exposed=false, tag="work")
	// 3. "public-personal" (exposed=true, tag="personal")

	createIndexHelper := func(name string, exposed bool, tags []string, desc string) {
		idxDir := filepath.Join(tmpDir, name)
		os.MkdirAll(idxDir, 0755)
		meta := gleann.IndexMeta{
			Name:           name,
			Backend:        "hnsw",
			EmbeddingModel: "bge-m3",
			NumPassages:    10,
			Tags:           tags,
			Description:    desc,
			MCPExposed:     &exposed,
		}
		data, _ := json.Marshal(meta)
		os.WriteFile(filepath.Join(idxDir, name+".meta.json"), data, 0644)
	}

	createIndexHelper("public-work", true, []string{"work"}, "Public work repo")
	createIndexHelper("private-work", false, []string{"work"}, "Secret work repo")
	createIndexHelper("public-personal", true, []string{"personal"}, "Personal notes")

	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	ctx := context.Background()

	// 1. handleList without GLEANN_TAGS
	res, err := srv.handleList(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleList failed: %v", err)
	}
	content := res.Content[0].(mcp.TextContent).Text
	if strings.Contains(content, "private-work") {
		t.Errorf("handleList should not contain private-work, got: %s", content)
	}
	if !strings.Contains(content, "public-work") || !strings.Contains(content, "public-personal") {
		t.Errorf("handleList missing expected indexes, got: %s", content)
	}
	if !strings.Contains(content, "tags=[work]") || !strings.Contains(content, "Public work repo") {
		t.Errorf("handleList should render tags and description, got: %s", content)
	}

	// 2. handleList with GLEANN_TAGS="work"
	os.Setenv("GLEANN_TAGS", "work")
	defer os.Unsetenv("GLEANN_TAGS")

	resScoped, err := srv.handleList(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleList scoped failed: %v", err)
	}
	contentScoped := resScoped.Content[0].(mcp.TextContent).Text
	if strings.Contains(contentScoped, "public-personal") {
		t.Errorf("scoped handleList should not contain public-personal, got: %s", contentScoped)
	}
	if !strings.Contains(contentScoped, "public-work") {
		t.Errorf("scoped handleList missing public-work, got: %s", contentScoped)
	}

	// 3. getSearcher should deny private index
	_, err = srv.getSearcher("private-work")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("expected access denied for private index, got: %v", err)
	}

	// 4. getSearcher should deny non-matching tag when GLEANN_TAGS is set
	_, err = srv.getSearcher("public-personal")
	if err == nil || !strings.Contains(err.Error(), "does not match required tags") {
		t.Errorf("expected tag mismatch error, got: %v", err)
	}
}
