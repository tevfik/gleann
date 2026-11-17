package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// h2UnopenableMemoryDir points GLEANN_MEMORY_DIR below a regular file, so
// the memory store cannot be created on any platform.
func h2UnopenableMemoryDir(t *testing.T) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(f, "memory"))
}

func TestH2MemoryTools_StoreUnavailable(t *testing.T) {
	h2UnopenableMemoryDir(t)
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom"})
	defer srv.Close()

	cases := []struct {
		name string
		h    toolHandler
		args map[string]any
		want string
	}{
		{"remember", srv.handleMemoryRemember, map[string]any{"content": "use WAL mode"}, "open memory store"},
		{"forget", srv.handleMemoryForget, map[string]any{"id_or_query": "WAL"}, "open memory store"},
		{"search", srv.handleMemorySearch, map[string]any{"query": "WAL", "scope": "all"}, "open memory store"},
		{"list", srv.handleMemoryList, map[string]any{"scope": "*"}, "open memory store"},
		{"context", srv.handleMemoryContext, map[string]any{"scope": "all"}, "open memory store"},
		{"failure add", srv.handleMemoryFailureAdd, map[string]any{"command": "go test", "error": "boom"}, "open memory store"},
		{"failure list", srv.handleMemoryFailureList, map[string]any{}, "open memory store"},
		{"inject", srv.handleInjectKG, map[string]any{"index": "kg"}, `open memory store "kg"`},
		{"delete entity", srv.handleDeleteEntity, map[string]any{"index": "kg", "id": "n1"}, `open memory store "kg"`},
		{"traverse", srv.handleTraverseKG, map[string]any{"index": "kg", "start_id": "n1"}, `open memory store "kg"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h2Call(t, tc.h, tc.args)
			if !res.IsError || !strings.Contains(extractText(res), tc.want) {
				t.Fatalf("got error=%v %q, want %q", res.IsError, extractText(res), tc.want)
			}
		})
	}
}

// A memory store closed under the server (e.g. by a concurrent shutdown)
// turns every operation into a tool error instead of a panic.
func TestH2MemoryTools_ClosedStore(t *testing.T) {
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom"})
	defer srv.Close()

	mgr, err := srv.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.memPool.get("kg"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := srv.memPool.store.Close(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		h    toolHandler
		args map[string]any
		want string
	}{
		{"remember", srv.handleMemoryRemember, map[string]any{"content": "use WAL mode"}, "remember failed"},
		{"forget", srv.handleMemoryForget, map[string]any{"id_or_query": "WAL"}, ""},
		{"search scoped", srv.handleMemorySearch, map[string]any{"query": "WAL", "scope": "repo"}, "search failed"},
		{"search all", srv.handleMemorySearch, map[string]any{"query": "WAL", "scope": "all"}, "search failed"},
		{"list scoped", srv.handleMemoryList, map[string]any{"scope": "repo"}, "list failed"},
		{"list all", srv.handleMemoryList, map[string]any{"scope": "all"}, "list failed"},
		{"context", srv.handleMemoryContext, map[string]any{"scope": "all"}, "build context"},
		{"failure add", srv.handleMemoryFailureAdd, map[string]any{"command": "go test", "error": "boom"}, "record failure"},
		{"failure list", srv.handleMemoryFailureList, map[string]any{}, "list failures"},
		{"inject", srv.handleInjectKG, map[string]any{"index": "kg", "nodes": []any{map[string]any{"id": "n1", "type": "concept"}}}, "inject failed"},
		{"delete entity", srv.handleDeleteEntity, map[string]any{"index": "kg", "id": "n1"}, "delete entity"},
		{"traverse", srv.handleTraverseKG, map[string]any{"index": "kg", "start_id": "n1"}, "traverse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h2Call(t, tc.h, tc.args)
			if !res.IsError || !strings.Contains(extractText(res), tc.want) {
				t.Fatalf("got error=%v %q, want %q", res.IsError, extractText(res), tc.want)
			}
		})
	}
}

func TestH2InjectKG_RejectsMalformedPayloads(t *testing.T) {
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom"})
	defer srv.Close()
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"index": "kg", "nodes": "not a list"}, "invalid nodes"},
		{map[string]any{"index": "kg", "edges": 42.0}, "invalid edges"},
		{map[string]any{"index": "../kg"}, `open memory store "../kg"`},
	} {
		res := h2Call(t, srv.handleInjectKG, tc.args)
		if !res.IsError || !strings.Contains(extractText(res), tc.want) {
			t.Errorf("%v: got %q, want %q", tc.args, extractText(res), tc.want)
		}
	}
}
