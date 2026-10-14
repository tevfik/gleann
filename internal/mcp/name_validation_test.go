package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleSync_RejectsUnsafeIndexNames(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", tmpDir)
	srv := NewServer(Config{IndexDir: tmpDir, Version: "test"})
	defer srv.Close()

	ran := false
	srv.syncRunner = func(ctx context.Context, opts SyncOptions) (string, error) {
		ran = true
		return "ok", nil
	}

	for _, name := range []string{"..", "../outside", "-x", "--index-dir=/tmp", "a/b"} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"index": name, "docs_dir": tmpDir}
		res, err := srv.handleSync(context.Background(), req)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", name, err)
		}
		if res == nil || !res.IsError {
			t.Errorf("%q: expected tool error result", name)
		}
	}
	if ran {
		t.Fatal("sync runner must not be invoked for unsafe names")
	}
}

func TestGetSearcher_RejectsUnsafeIndexNames(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", tmpDir)
	srv := NewServer(Config{IndexDir: tmpDir, Version: "test"})
	defer srv.Close()

	for _, name := range []string{"..", "../x", "-x"} {
		if _, err := srv.getSearcher(name); err == nil {
			t.Errorf("getSearcher(%q) should fail", name)
		}
	}
}

func TestSanitizeIndexName(t *testing.T) {
	cases := map[string]string{
		"my-project": "my-project",
		"my project": "my-project",
		".hidden":    "hidden",
		"-flag":      "flag",
		"a:b@c":      "a-b-c",
		"...":        "",
	}
	for in, want := range cases {
		got := sanitizeIndexName(in)
		if got != want {
			t.Errorf("sanitizeIndexName(%q) = %q, want %q", in, got, want)
		}
		if got != "" && gleann.ValidateName(got) != nil {
			t.Errorf("sanitizeIndexName(%q) = %q is not a valid name", in, got)
		}
	}
}
