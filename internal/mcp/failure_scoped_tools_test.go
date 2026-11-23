package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

// The failure tools advertise the scope argument and memory_context keeps the
// distinct symbols argument.
func TestFailureToolScopedSchemas(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	add := s.buildMemoryFailureAddTool()
	if _, ok := add.InputSchema.Properties["scope"]; !ok {
		t.Error("memory_failure_add schema should include 'scope'")
	}
	list := s.buildMemoryFailureListTool()
	if _, ok := list.InputSchema.Properties["scope"]; !ok {
		t.Error("memory_failure_list schema should include 'scope'")
	}
	ctxTool := s.buildMemoryContextTool()
	if _, ok := ctxTool.InputSchema.Properties["symbols"]; !ok {
		t.Error("memory_context schema should include 'symbols'")
	}
}

// An explicit scope isolates failures; "all" lists every scope.
func TestMemoryFailureAddListScoped(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	add := func(scope, command, errText string) {
		t.Helper()
		args := map[string]any{"command": command, "error": errText, "fix": "retry"}
		if scope != "" {
			args["scope"] = scope
		}
		res, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(args))
		if err != nil || res.IsError {
			t.Fatalf("add err=%v res=%+v", err, res)
		}
	}
	add("repo-a", "go test ./pkg/a", "boom a")
	add("repo-b", "go test ./pkg/b", "boom b")

	listText := func(scope string) string {
		t.Helper()
		args := map[string]any{}
		if scope != "" {
			args["scope"] = scope
		}
		res, err := s.handleMemoryFailureList(ctx, mkCallArgs(args))
		if err != nil || res.IsError {
			t.Fatalf("list err=%v res=%+v", err, res)
		}
		return extractText(res)
	}

	if txt := listText("repo-a"); !strings.Contains(txt, "1 recorded failure") || !strings.Contains(txt, "boom a") || strings.Contains(txt, "boom b") {
		t.Fatalf("repo-a list = %q", txt)
	}
	if txt := listText("repo-b"); !strings.Contains(txt, "boom b") || strings.Contains(txt, "boom a") {
		t.Fatalf("repo-b list = %q", txt)
	}
	// "all" and "*" see both.
	for _, all := range []string{"all", "*"} {
		if txt := listText(all); !strings.Contains(txt, "2 recorded failure") {
			t.Fatalf("scope %q list = %q, want 2 failures", all, txt)
		}
	}
}

// Without a scope argument the tools default to the detected repository scope,
// so an added failure is visible to a default list but not to a foreign scope.
func TestMemoryFailureDefaultRepoScope(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	res, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go test", "error": "boom", "fix": "retry",
	}))
	if err != nil || res.IsError {
		t.Fatalf("add err=%v res=%+v", err, res)
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	want := memory.DetectRepoScope()
	fs, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Scope != want {
		t.Fatalf("default scope = %v, want %q", fs, want)
	}

	// Default list sees it.
	res, err = s.handleMemoryFailureList(ctx, mkCallArgs(map[string]any{}))
	if err != nil || res.IsError {
		t.Fatalf("list err=%v res=%+v", err, res)
	}
	if !strings.Contains(extractText(res), "1 recorded failure") {
		t.Fatalf("default list should see the failure, got %q", extractText(res))
	}
	// A foreign scope does not.
	res, err = s.handleMemoryFailureList(ctx, mkCallArgs(map[string]any{"scope": "some-other-repo"}))
	if err != nil || res.IsError {
		t.Fatalf("list err=%v res=%+v", err, res)
	}
	if !strings.Contains(extractText(res), "No failures recorded") {
		t.Fatalf("foreign scope should be empty, got %q", extractText(res))
	}
}

// memory_context surfaces a failure by symbol even when paths is empty, and
// does not leak it across scopes.
func TestMemoryContextSymbolsOnlyScoped(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	res, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go build ./pkg/a",
		"error":   "undefined: Thing",
		"fix":     "import thing",
		"symbols": []any{"github.com/org/repo-a/pkg.Thing"},
		"scope":   "repo-a",
	}))
	if err != nil || res.IsError {
		t.Fatalf("add err=%v res=%+v", err, res)
	}

	// symbols only, paths empty, matching scope → failure surfaces.
	res, err = s.handleMemoryContext(ctx, mkCallArgs(map[string]any{
		"scope":   "repo-a",
		"symbols": []any{"Thing"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("context err=%v res=%+v", err, res)
	}
	txt := extractText(res)
	if !strings.Contains(txt, "<failure_memory>") || !strings.Contains(txt, "import thing") {
		t.Fatalf("symbols-only context missing failure:\n%s", txt)
	}

	// Other scope must not see it.
	res, err = s.handleMemoryContext(ctx, mkCallArgs(map[string]any{
		"scope":   "repo-b",
		"symbols": []any{"Thing"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("context err=%v res=%+v", err, res)
	}
	if strings.Contains(extractText(res), "import thing") {
		t.Fatalf("repo-b context leaked repo-a failure:\n%s", extractText(res))
	}

	// "all" scope sees it by symbol.
	res, err = s.handleMemoryContext(ctx, mkCallArgs(map[string]any{
		"scope":   "all",
		"symbols": []any{"Thing"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("context err=%v res=%+v", err, res)
	}
	if !strings.Contains(extractText(res), "import thing") {
		t.Fatalf("all-scope symbols context missing failure:\n%s", extractText(res))
	}
}

// ── v2 repair, finding 4: memory_failure_add maps all/* to global ────────────

// add with scope "all"/"*" (any case/whitespace) must record a GLOBAL block
// that the default (repo-scoped) list can see — never a literal "all" scope.
func TestV2MCPFailureAddScopeAllGlobal(t *testing.T) {
	t.Chdir(t.TempDir()) // no git repo: DetectRepoScope falls back to dir name
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	for i, scope := range []string{"all", "*", "ALL", " * "} {
		res, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
			"command": "go build",
			"error":   fmt.Sprintf("zz mcp global probe %d", i),
			"fix":     "retry",
			"scope":   scope,
		}))
		if err != nil || res.IsError {
			t.Fatalf("add scope=%q err=%v res=%+v", scope, err, res)
		}
	}
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 4 {
		t.Fatalf("ListFailures = %d blocks, want 4", len(fs))
	}
	for _, b := range fs {
		if b.Scope != "" {
			t.Fatalf("sentinel add stored scope %q, want global \"\" (block %+v)", b.Scope, b)
		}
	}
	// Default list (detected repo scope) sees global failures.
	res, err := s.handleMemoryFailureList(ctx, mkCallArgs(map[string]any{}))
	if err != nil || res.IsError {
		t.Fatalf("list err=%v res=%+v", err, res)
	}
	if txt := extractText(res); !strings.Contains(txt, "4 recorded failure") {
		t.Fatalf("default list should see the 4 global failures, got %q", txt)
	}

	// Slash-spelled scopes are normalized on add.
	res, err = s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go vet", "error": "zz mcp slash probe", "fix": "retry", "scope": "repo-a/",
	}))
	if err != nil || res.IsError {
		t.Fatalf("add repo-a/ err=%v res=%+v", err, res)
	}
	fs, _ = mgr.ListFailuresScoped("repo-a")
	found := false
	for _, b := range fs {
		if b.Content == "zz mcp slash probe" && b.Scope == "repo-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("repo-a/ add not stored under normalized repo-a: %+v", fs)
	}
}

// ── v2 repair, finding 7: scalar string symbols/paths arguments ──────────────

// memory_context must accept a bare comma-separated string for symbols/paths,
// and memory_failure_add must parse scalar paths the same way.
func TestV2MCPContextScalarStringArgs(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	res, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go build ./pkg/a",
		"error":   "undefined: Alpha",
		"fix":     "import alpha",
		"scope":   "repo-a",
		"symbols": []any{"github.com/org/repo-a/pkg.Alpha"},
		"paths":   []any{"pkg/a.go", "pkg/b.go"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("seed err=%v res=%+v", err, res)
	}

	for _, args := range []map[string]any{
		{"scope": "repo-a", "symbols": "Alpha"},
		{"scope": "repo-a", "symbols": "pkg.Alpha"},
		{"scope": "repo-a", "paths": "pkg/a.go, pkg/b.go"},
		{"scope": "repo-a", "paths": "a.go"},
		{"scope": "repo-a", "symbols": []any{"Alpha"}},
	} {
		res, err := s.handleMemoryContext(ctx, mkCallArgs(args))
		if err != nil || res.IsError {
			t.Fatalf("context %v err=%v res=%+v", args, err, res)
		}
		txt := extractText(res)
		if !strings.Contains(txt, "<failure_memory>") || !strings.Contains(txt, "import alpha") {
			t.Fatalf("context args %v missing failure:\n%s", args, txt)
		}
	}

	// Scalar comma-separated paths on add are split, trimmed, non-empty.
	res, err = s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go vet", "error": "zz scalar paths probe", "scope": "repo-a",
		"paths": "pkg/x.go, pkg/y.go",
	}))
	if err != nil || res.IsError {
		t.Fatalf("add scalar paths err=%v res=%+v", err, res)
	}
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := mgr.ListFailures()
	var block *memory.Block
	for i := range fs {
		if fs[i].Content == "zz scalar paths probe" {
			block = &fs[i]
		}
	}
	if block == nil {
		t.Fatal("scalar-paths failure not recorded")
	}
	if len(block.Paths) != 2 || block.Paths[0] != "pkg/x.go" || block.Paths[1] != "pkg/y.go" {
		t.Fatalf("scalar paths = %v, want [pkg/x.go pkg/y.go]", block.Paths)
	}
}

// ── v3 repair, finding 1: memory_remember normalizes the block scope ─────────

func TestV3MCPRememberNormalizesScope(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "zz v3 mcp padded", "scope": "repo-a/", "repo": "/repo-a/",
	}))
	if err != nil || res.IsError {
		t.Fatalf("memory_remember padded scope failed: %v %+v", err, res)
	}
	got, err := mgr.ListScoped("repo-a", memory.TierLong)
	if err != nil || len(got) != 1 || got[0].Scope != "repo-a" || got[0].Repo != "repo-a" {
		t.Fatalf("ListScoped(repo-a) = %v (err %v), want one normalized repo-a block", got, err)
	}

	// A sentinel scope is global, never a literal "all" block.
	res, err = s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "zz v3 mcp sentinel", "scope": " ALL ",
	}))
	if err != nil || res.IsError {
		t.Fatalf("memory_remember sentinel scope failed: %v %+v", err, res)
	}
	got, err = mgr.ListScoped("repo-b", memory.TierLong)
	if err != nil || len(got) != 1 || got[0].Scope != "" || got[0].Content != "zz v3 mcp sentinel" {
		t.Fatalf("repo-b view = %v (err %v), want only the global sentinel block", got, err)
	}

	// A padded repo without an explicit scope defaults the scope to the repo.
	res, err = s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "zz v3 mcp repo default", "repo": "/repo-b/",
	}))
	if err != nil || res.IsError {
		t.Fatalf("memory_remember padded repo failed: %v %+v", err, res)
	}
	got, err = mgr.ListScoped("repo-b", memory.TierLong)
	if err != nil || len(got) != 2 {
		t.Fatalf("repo-b view after repo default = %v (err %v), want 2 blocks", got, err)
	}
	for _, b := range got {
		if b.Content == "zz v3 mcp repo default" && (b.Scope != "repo-b" || b.Repo != "repo-b") {
			t.Fatalf("repo-default block stored Scope=%q Repo=%q, want repo-b/repo-b", b.Scope, b.Repo)
		}
	}
}

// ── v3 repair, finding 3: search/list sentinels use the shared helper ────────

func TestV3MCPSearchListSentinelSpellings(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RememberBlock(&memory.Block{Tier: memory.TierLong, Label: "fact", Content: "zz v3 alpha marker", Scope: "repo-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RememberBlock(&memory.Block{Tier: memory.TierLong, Label: "fact", Content: "zz v3 beta marker", Scope: "repo-b"}); err != nil {
		t.Fatal(err)
	}

	// Every sentinel spelling widens search and list to all scopes.
	for _, sentinel := range []string{"all", "ALL", " All ", "*", " * "} {
		res, err := s.handleMemorySearch(ctx, mkCallArgs(map[string]any{"query": "zz v3 marker", "scope": sentinel}))
		text := extractText(res)
		if err != nil || res.IsError || !strings.Contains(text, "alpha") || !strings.Contains(text, "beta") {
			t.Fatalf("memory_search scope %q must see both scopes (err %v):\n%s", sentinel, err, text)
		}
		res, err = s.handleMemoryList(ctx, mkCallArgs(map[string]any{"scope": sentinel}))
		text = extractText(res)
		if err != nil || res.IsError || !strings.Contains(text, "alpha") || !strings.Contains(text, "beta") {
			t.Fatalf("memory_list scope %q must see both scopes (err %v):\n%s", sentinel, err, text)
		}
	}

	// Padded concrete scopes still isolate, in search and list.
	res, err := s.handleMemorySearch(ctx, mkCallArgs(map[string]any{"query": "zz v3 marker", "scope": "repo-a/"}))
	text := extractText(res)
	if err != nil || res.IsError || !strings.Contains(text, "alpha") || strings.Contains(text, "beta") {
		t.Fatalf("memory_search scope repo-a/ leaked repo-b (err %v):\n%s", err, text)
	}
	res, err = s.handleMemoryList(ctx, mkCallArgs(map[string]any{"scope": "/repo-b/"}))
	text = extractText(res)
	if err != nil || res.IsError || !strings.Contains(text, "beta") || strings.Contains(text, "alpha") {
		t.Fatalf("memory_list scope /repo-b/ leaked repo-a (err %v):\n%s", err, text)
	}
}
