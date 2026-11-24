//go:build linux

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// openFilesUnder counts this process's open file descriptors for files under
// dir. On Windows, t.TempDir() cleanup (os.RemoveAll) fails with "The process
// cannot access the file because it is being used by another process" when
// any file under the temp dir is still open at test end. On Linux the removal
// succeeds anyway, so counting /proc/self/fd entries is the portable proof.
func openFilesUnder(t testing.TB, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	var out []string
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && strings.HasPrefix(target, dir+string(filepath.Separator)) {
			out = append(out, target)
		}
	}
	return out
}

// TestTestServerReleasesBlockMemoryStore proves the testServer helper closes
// its Server: the gleann_sync success path (handleSync) opens the block
// memory store under GLEANN_MEMORY_DIR, and the helper's t.Cleanup(srv.Close)
// must release memory.sqlite(+wal/shm) before cleanups run. Without that
// Close the store stays open and t.TempDir removal fails on Windows.
func TestTestServerReleasesBlockMemoryStore(t *testing.T) {
	memDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", memDir)
	// Registered before serverWithSource: t.Cleanup runs LIFO, so this check
	// runs after the helper's srv.Close cleanup (if the helper has one) and
	// before memDir itself is removed.
	t.Cleanup(func() {
		if open := openFilesUnder(t, memDir); len(open) > 0 {
			t.Errorf("memory store handles still open at test end: %v", open)
		}
	})

	allowed := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", allowed)
	srv, src := serverWithSource(t)
	srv.syncRunner = func(ctx context.Context, opts SyncOptions) (string, error) {
		return "ok", nil
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"index": "src",
		"files": []interface{}{filepath.Join(src, "ok.go")},
	}
	res, err := srv.handleSync(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("sync of a file inside the source directory refused: %v", res.Content)
	}
}

// newFederatedServer builds three indexes with gleann.Builder and preloads two
// searchers. After Server.Close nothing under the index directory may stay
// open: on Windows t.TempDir cleanup fails on an open index database.
func TestFederatedServerReleasesIndexFiles(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s := newFederatedServer(t)
	var req mcp.ReadResourceRequest
	req.Params.URI = "gleann://indexes"
	if _, err := s.handleIndexListResource(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if open := openFilesUnder(t, s.config.IndexDir); len(open) > 0 {
		t.Errorf("index files still open after Server.Close: %v", open)
	}
}
