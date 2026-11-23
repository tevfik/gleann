//go:build linux

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

// openDBFDs counts this process's open file descriptors on *.db files under dir.
func openDBFDs(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	n := 0
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && strings.HasPrefix(target, dir) && strings.HasSuffix(target, ".db") {
			n++
		}
	}
	return n
}

// The impact handler borrows a cached searcher that keeps the index database
// open between calls (by design). Server.Close must release it, so that the
// index directory can be removed (required on Windows).
func TestImpact_ServerCloseReleasesIndexDB(t *testing.T) {
	env := analysisgolden.BuildIndexFormat2(t)
	s := NewServer(Config{IndexDir: env.IndexDir})
	closed := false
	t.Cleanup(func() {
		if !closed {
			s.Close()
		}
	})

	callTool(t, s.handleImpact, map[string]interface{}{
		"index":  analysisgolden.IndexName,
		"symbol": "example.com/fixture/service.Service.Load",
	})
	if openDBFDs(t, env.IndexDir) == 0 {
		t.Skip("handler did not keep the db open; nothing to release")
	}
	s.closeGraphPool()
	if openDBFDs(t, env.IndexDir) == 0 {
		t.Fatal("closeGraphPool alone released the db; test no longer demonstrates the searcher handle")
	}
	s.Close()
	closed = true
	if n := openDBFDs(t, env.IndexDir); n != 0 {
		t.Fatalf("%d index db handle(s) still open after Server.Close", n)
	}
}
