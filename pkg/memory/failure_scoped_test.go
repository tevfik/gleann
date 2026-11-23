package memory

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordScoped is a helper that records a failure bound to a scope/repo.
func recordScoped(t *testing.T, mgr *Manager, scope, repo string, f Failure) *Block {
	t.Helper()
	b, err := mgr.RecordFailureScoped(scope, repo, f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Two scopes recording the same command+error must stay two blocks, and each
// scope only sees its own (plus global) failures.
func TestFailureScopedIsolation(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "repo-a", Failure{Command: "go test ./pkg/a", Error: "boom a", Fix: "fix a"})
	recordScoped(t, mgr, "repo-b", "repo-b", Failure{Command: "go test ./pkg/b", Error: "boom b", Fix: "fix b"})

	a, err := mgr.ListFailuresScoped("repo-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].Metadata[FailureMetaCommand] != "go test ./pkg/a" {
		t.Fatalf("repo-a failures = %v, want the pkg/a failure", a)
	}
	b, err := mgr.ListFailuresScoped("repo-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1 || b[0].Metadata[FailureMetaCommand] != "go test ./pkg/b" {
		t.Fatalf("repo-b failures = %v, want the pkg/b failure", b)
	}
	all, err := mgr.ListFailuresScoped("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped list = %d, want 2", len(all))
	}
}

// The same command+error in two scopes is two blocks; a repeat within one scope
// deduplicates (Confirms++). This is the core cross-scope dedup isolation.
func TestFailureScopedDedupPerScope(t *testing.T) {
	mgr := newFailureTestManager(t)
	f := Failure{Command: "go test", Error: "panic at block.go:42", Fix: "nil check"}
	recordScoped(t, mgr, "repo-a", "repo-a", f)
	recordScoped(t, mgr, "repo-b", "repo-b", f)

	if n := len(mustListFailures(t, mgr)); n != 2 {
		t.Fatalf("same failure in two scopes = %d blocks, want 2", n)
	}
	// Repeat in repo-a reinforces, not duplicates.
	b := recordScoped(t, mgr, "repo-a", "repo-a", f)
	if b.Confirms != 2 {
		t.Fatalf("repeat Confirms = %d, want 2", b.Confirms)
	}
	a, _ := mgr.ListFailuresScoped("repo-a")
	if len(a) != 1 || a[0].Confirms != 2 {
		t.Fatalf("repo-a after repeat = %v, want 1 block Confirms 2", a)
	}
	bb, _ := mgr.ListFailuresScoped("repo-b")
	if len(bb) != 1 || bb[0].Confirms != 1 {
		t.Fatalf("repo-b must be untouched, got %v", bb)
	}
}

func mustListFailures(t *testing.T, mgr *Manager) []Block {
	t.Helper()
	fs, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// An empty scope with a non-empty repo defaults the scope to the repo, so the
// failure is isolated to that repo and normalized.
func TestFailureScopeDefaultsFromRepo(t *testing.T) {
	mgr := newFailureTestManager(t)
	b, err := mgr.RecordFailure(Failure{Repo: "github.com/org/repo", Command: "go build", Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Scope != "github.com/org/repo" {
		t.Fatalf("Scope = %q, want defaulted from Repo", b.Scope)
	}
	// Normalization: surrounding slashes/whitespace are trimmed.
	b2, err := mgr.RecordFailure(Failure{Scope: "  /github.com/org/repo/ ", Command: "go vet", Error: "bam"})
	if err != nil {
		t.Fatal(err)
	}
	if b2.Scope != "github.com/org/repo" {
		t.Fatalf("normalized Scope = %q, want github.com/org/repo", b2.Scope)
	}
	// The defaulted and the explicit scope dedup together (same normalized scope).
	if _, err := mgr.RecordFailureScoped("github.com/org/repo", "", Failure{Command: "go build", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	got, _ := mgr.ListFailuresScoped("github.com/org/repo")
	var build *Block
	for i := range got {
		if got[i].Metadata[FailureMetaCommand] == "go build" {
			build = &got[i]
		}
	}
	if build == nil || build.Confirms != 2 {
		t.Fatalf("defaulted-scope and explicit-scope recordings must dedup, got %v", got)
	}
}

// Empty-scope IDs stay deterministic and identical to the legacy hash shape
// (command+signature only), so existing global failures are not re-keyed.
func TestFailureEmptyScopeIDDeterminism(t *testing.T) {
	mgr := newFailureTestManager(t)
	b1, err := mgr.RecordFailure(Failure{Command: "go test", Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	want := failureBlockID("go test", NormalizeErrorSignature("boom"), "")
	if b1.ID != want {
		t.Fatalf("empty-scope ID = %s, want %s", b1.ID, want)
	}
	// A scoped recording of the same command+error must NOT collide.
	b2 := recordScoped(t, mgr, "repo-a", "", Failure{Command: "go test", Error: "boom"})
	if b2.ID == b1.ID {
		t.Fatalf("scoped and global failure share ID %s", b1.ID)
	}
}

// Command matching is scope-aware: a command recorded in repo-a is invisible to
// repo-b, and an unscoped query sees it.
func TestFailureScopedMatchByCommand(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{Command: "go test ./pkg/a", Error: "a"})
	recordScoped(t, mgr, "repo-b", "", Failure{Command: "go test ./pkg/c", Error: "c"})

	got, err := mgr.FailuresForScoped("repo-a", "go test ./pkg/a", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Metadata[FailureMetaCommand] != "go test ./pkg/a" {
		t.Fatalf("repo-a command match = %v", got)
	}
	got, _ = mgr.FailuresForScoped("repo-b", "go test ./pkg/a", nil, nil)
	if len(got) != 0 {
		t.Fatalf("repo-b must not see repo-a command, got %v", got)
	}
	got, _ = mgr.FailuresForScoped("", "go test ./pkg/a", nil, nil)
	if len(got) != 1 {
		t.Fatalf("unscoped command match = %d, want 1", len(got))
	}
}

// Path matching is repo-relative and scope-aware.
func TestFailureScopedMatchByPath(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{Command: "go test", Error: "a", Paths: []string{"pkg/a/alpha.go"}})
	recordScoped(t, mgr, "repo-b", "", Failure{Command: "go test", Error: "b", Paths: []string{"pkg/b/beta.go"}})

	got, err := mgr.FailuresForScoped("repo-a", "", []string{"pkg/a/alpha.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Scope != "repo-a" {
		t.Fatalf("repo-a path match = %v", got)
	}
	got, _ = mgr.FailuresForScoped("repo-a", "", []string{"pkg/b/beta.go"}, nil)
	if len(got) != 0 {
		t.Fatalf("repo-a must not match repo-b path, got %v", got)
	}
}

// Base-name fallback works within the same repo/scope but never leaks a
// basename across repos (here modeled via an ancestor scope and a global block).
func TestFailureScopedBasenameNoCrossRepoLeak(t *testing.T) {
	mgr := newFailureTestManager(t)
	// Same-repo positive: block scoped repo-b, query repo-b by basename.
	recordScoped(t, mgr, "repo-b", "", Failure{Command: "go build", Error: "b", Paths: []string{"deep/dir/main.go"}})
	got, err := mgr.FailuresForScoped("repo-b", "", []string{"main.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("same-repo basename should match, got %v", got)
	}

	// Ancestor scope block must NOT leak its basename into a descendant query.
	recordScoped(t, mgr, "myorg", "", Failure{Command: "go build", Error: "org", Paths: []string{"org/main.go"}})
	got, _ = mgr.FailuresForScoped("myorg/repo-b", "", []string{"main.go"}, nil)
	for _, b := range got {
		if b.Scope == "myorg" {
			t.Fatalf("ancestor basename leaked into descendant scope: %v", got)
		}
	}

	// A global (no repo) block is visible everywhere but its basename must not
	// match a scoped query; only an exact path match surfaces it.
	if _, err := mgr.RecordFailure(Failure{Command: "go build", Error: "global", Paths: []string{"glob/main.go"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = mgr.FailuresForScoped("repo-b", "", []string{"main.go"}, nil)
	for _, b := range got {
		if b.Scope == "" {
			t.Fatalf("global basename leaked into scoped query: %v", got)
		}
	}
	got, _ = mgr.FailuresForScoped("repo-b", "", []string{"glob/main.go"}, nil)
	var foundGlobal bool
	for _, b := range got {
		if b.Scope == "" && len(b.Paths) > 0 && b.Paths[0] == "glob/main.go" {
			foundGlobal = true
		}
	}
	if !foundGlobal {
		t.Fatalf("global exact path should surface for a scoped query, got %v", got)
	}
}

// A block recorded at an ancestor scope but tagged with the queried repo (Repo)
// allows basename fallback via the Repo match.
func TestFailureScopedBasenameViaRepo(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "myorg", "myorg/repo-b", Failure{Command: "go build", Error: "org", Paths: []string{"org/main.go"}})
	got, err := mgr.FailuresForScoped("myorg/repo-b", "", []string{"main.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Repo-tagged ancestor block should basename-match its repo, got %v", got)
	}
}

// Unscoped queries keep the legacy cleaned-path-or-basename behavior.
func TestFailureUnscopedBasenameLegacy(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{Command: "go build", Error: "a", Paths: []string{"pkg/a/main.go"}})
	got, err := mgr.FailuresForScoped("", "", []string{"main.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("unscoped basename (legacy) should match, got %v", got)
	}
}

// Symbol matching: exact, FQN suffix in either direction, and false-positive
// guards (single char, raw substring). Symbols are a distinct query from paths.
func TestFailureScopedSymbolMatching(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{
		Command: "go build", Error: "a",
		Symbols: []string{"github.com/org/repo-a/pkg.Foo"},
	})

	cases := []struct {
		name    string
		symbols []string
		want    int
	}{
		{"exact FQN", []string{"github.com/org/repo-a/pkg.Foo"}, 1},
		{"short suffix", []string{"Foo"}, 1},
		{"mid suffix", []string{"pkg.Foo"}, 1},
		{"unrelated FQN", []string{"other/pkg.Foo"}, 0},
		{"single char", []string{"F"}, 0},
		{"substring", []string{"oo"}, 0},
		{"wrong symbol", []string{"Bar"}, 0},
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mgr.FailuresForScoped("repo-a", "", nil, tc.symbols)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("symbols %v = %d failures, want %d", tc.symbols, len(got), tc.want)
			}
		})
	}
}

// A symbol query must not leak across scopes.
func TestFailureScopedSymbolNoCrossScopeLeak(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{Command: "go build", Error: "a", Symbols: []string{"pkg.Foo"}})
	got, err := mgr.FailuresForScoped("repo-b", "", nil, []string{"Foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("repo-b must not see repo-a symbol, got %v", got)
	}
}

// BuildTaskContextFor surfaces a failure by symbol even when paths is empty,
// and keeps it out of other scopes.
func TestBuildTaskContextForSymbolsOnly(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{
		Command: "go build ./pkg/a", Error: "undefined: Thing", Fix: "import thing",
		Symbols: []string{"github.com/org/repo-a/pkg.Thing"},
	})

	// paths empty, symbols only → failure surfaces.
	cw, err := mgr.BuildTaskContextFor("repo-a", "", nil, []string{"Thing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 1 {
		t.Fatalf("symbols-only task should surface 1 failure, got %d", len(cw.Failures))
	}
	if !strings.Contains(cw.Render(), "import thing") {
		t.Fatalf("rendered context missing fix:\n%s", cw.Render())
	}

	// Other scope does not see it.
	cw, err = mgr.BuildTaskContextFor("repo-b", "", nil, []string{"Thing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 0 {
		t.Fatalf("repo-b symbols-only task must see 0 failures, got %d", len(cw.Failures))
	}

	// No command/paths/symbols → no failure section.
	cw, err = mgr.BuildTaskContextFor("repo-a", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Failures) != 0 {
		t.Fatalf("empty task must surface no failures, got %d", len(cw.Failures))
	}
}

// BuildTaskContextFor restricts the general context to the scope as well.
func TestBuildTaskContextForScopeFilter(t *testing.T) {
	mgr := newFailureTestManager(t)
	if _, err := mgr.AddScopedNote("repo-a", TierLong, "a", "alpha convention"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddScopedNote("repo-b", TierLong, "b", "beta convention"); err != nil {
		t.Fatal(err)
	}
	recordScoped(t, mgr, "repo-a", "", Failure{Command: "go test", Error: "boom", Fix: "retry"})

	cw, err := mgr.BuildTaskContextFor("repo-a", "go test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var long []string
	for _, b := range cw.LongTerm {
		if b.Label != FailureLabel {
			long = append(long, b.Content)
		}
	}
	joined := strings.Join(long, "|")
	if !strings.Contains(joined, "alpha") || strings.Contains(joined, "beta") {
		t.Fatalf("repo-a context = %v, want alpha not beta", long)
	}
	if len(cw.Failures) != 1 {
		t.Fatalf("repo-a task failures = %d, want 1", len(cw.Failures))
	}
}

// Command OR path OR symbol: a non-matching command still lets a path or symbol
// match surface the failure (legacy OR semantics preserved).
func TestFailuresForCommandOrPathOrSymbol(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{Command: "go build ./pkg/b", Error: "b", Paths: []string{"pkg/b/main.go"}})
	// Command does not match, but path does → still surfaced.
	got, err := mgr.FailuresForScoped("repo-a", "go test", []string{"pkg/b/main.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("command-mismatch + path-match should surface, got %v", got)
	}
	// Legacy unscoped FailuresFor keeps the same OR behavior.
	got, err = mgr.FailuresFor("go test", []string{"pkg/b/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("legacy FailuresFor OR semantics broken, got %v", got)
	}
}

// Hierarchical scope visibility: a descendant sees ancestor + global failures.
func TestFailureScopedHierarchy(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "myorg", "", Failure{Command: "go test", Error: "org"})
	recordScoped(t, mgr, "myorg/backend", "", Failure{Command: "go test", Error: "backend"})
	recordScoped(t, mgr, "other", "", Failure{Command: "go test", Error: "other"})

	got, err := mgr.ListFailuresScoped("myorg/backend/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("descendant should see org+backend failures, got %d: %v", len(got), got)
	}
	for _, b := range got {
		if b.Scope == "other" {
			t.Fatalf("sibling scope leaked: %v", got)
		}
	}
}

// NormalizeScope trims whitespace and surrounding slashes, preserves case.
func TestNormalizeScope(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"  github.com/org/repo  ": "github.com/org/repo",
		"/github.com/org/repo/":   "github.com/org/repo",
		"GitHub.com/Org/Repo":     "GitHub.com/Org/Repo",
	}
	for in, want := range cases {
		if got := NormalizeScope(in); got != want {
			t.Errorf("NormalizeScope(%q) = %q, want %q", in, got, want)
		}
	}
}

// symbolOverlaps unit checks for the false-positive guards and separator unity.
func TestSymbolOverlapsGuards(t *testing.T) {
	stored := []string{"github.com/org/repo/pkg.Foo"}
	yes := [][]string{
		{"Foo"}, {"pkg.Foo"}, {"github.com/org/repo/pkg.Foo"},
	}
	no := [][]string{
		{"F"}, {"oo"}, {"FooBar"}, {"Bar"}, {""}, nil,
	}
	for _, q := range yes {
		if !symbolOverlaps(stored, q) {
			t.Errorf("symbolOverlaps(%v) = false, want true", q)
		}
	}
	for _, q := range no {
		if symbolOverlaps(stored, q) {
			t.Errorf("symbolOverlaps(%v) = true, want false", q)
		}
	}
	// Rust/TS separators normalize to dots.
	if !symbolOverlaps([]string{"crate::module::Type::func"}, []string{"Type.func"}) {
		t.Error("Rust :: FQN suffix should match")
	}
	if !symbolOverlaps([]string{"module#Class.method"}, []string{"Class.method"}) {
		t.Error("TS # FQN suffix should match")
	}
}

// pathOverlaps uses slash-normalized cleaned paths.
func TestPathOverlapsSlashNormalization(t *testing.T) {
	if !pathOverlaps([]string{"pkg/a/block.go"}, []string{"pkg/a/block.go"}, false) {
		t.Error("exact cleaned path should match")
	}
	if !pathOverlaps([]string{"pkg/a/block.go"}, []string{"block.go"}, true) {
		t.Error("basename should match when allowed")
	}
	if pathOverlaps([]string{"pkg/a/block.go"}, []string{"block.go"}, false) {
		t.Error("basename must not match when disallowed")
	}
	if pathOverlaps([]string{"pkg/a/block.go"}, []string{"pkg/b/block.go"}, false) {
		t.Error("different dir must not exact-match")
	}
	if !pathOverlaps([]string{"pkg/a/block.go"}, []string{"pkg/b/block.go"}, true) {
		t.Error("different dir same basename should match when basename allowed")
	}
}

// ── v2 repair, finding 1: failureBlockID must be injective across scopes ─────

// v2LegacyFailureID reproduces the exact pre-scoping ID formula so tests can
// prove empty-scope IDs stay byte-compatible with earlier releases.
func v2LegacyFailureID(command, sig string) string {
	sum := sha256.Sum256([]byte("failure|" + normalizeCommand(command) + "|" + sig))
	return fmt.Sprintf("%x", sum)[:16]
}

// Order A: a scoped failure recorded first must not swallow a later global
// failure whose error signature ends with "|<scope>".
func TestV2FailureIDCollisionScopedThenUnscoped(t *testing.T) {
	mgr := newFailureTestManager(t)
	scoped, err := mgr.RecordFailureScoped("repo-a", "", Failure{Command: "go build", Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	global, err := mgr.RecordFailure(Failure{Command: "go build", Error: "boom|repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.ID == global.ID {
		t.Fatalf("scoped and unscoped failure share ID %s", scoped.ID)
	}
	all := mustListFailures(t, mgr)
	if len(all) != 2 {
		t.Fatalf("ListFailures = %d blocks, want 2 (scoped failure swallowed the global one)", len(all))
	}
	var gotScoped, gotGlobal *Block
	for i := range all {
		switch all[i].Scope {
		case "repo-a":
			gotScoped = &all[i]
		case "":
			gotGlobal = &all[i]
		}
	}
	if gotScoped == nil || gotGlobal == nil {
		t.Fatalf("want one repo-a block and one global block, got %+v", all)
	}
	if gotScoped.Confirms != 1 || gotGlobal.Confirms != 1 {
		t.Fatalf("confirms conflated: scoped=%d global=%d, want 1/1", gotScoped.Confirms, gotGlobal.Confirms)
	}
	if gotScoped.Content != "boom" || gotGlobal.Content != "boom|repo-a" {
		t.Fatalf("contents merged: scoped=%q global=%q", gotScoped.Content, gotGlobal.Content)
	}
}

// Order B: a global failure recorded first must not be mutated by a later
// scoped failure, and the scoped failure must not leak into the global row.
func TestV2FailureIDCollisionUnscopedThenScoped(t *testing.T) {
	mgr := newFailureTestManager(t)
	global, err := mgr.RecordFailure(Failure{Command: "go build", Error: "boom|repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := mgr.RecordFailureScoped("repo-a", "", Failure{Command: "go build", Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if global.ID == scoped.ID {
		t.Fatalf("scoped failure reused the global block ID %s", global.ID)
	}
	all := mustListFailures(t, mgr)
	if len(all) != 2 {
		t.Fatalf("ListFailures = %d blocks, want 2", len(all))
	}
	var gotScoped, gotGlobal *Block
	for i := range all {
		switch all[i].Scope {
		case "repo-a":
			gotScoped = &all[i]
		case "":
			gotGlobal = &all[i]
		}
	}
	if gotScoped == nil || gotGlobal == nil {
		t.Fatalf("want one repo-a block and one untouched global block, got %+v", all)
	}
	if gotGlobal.Confirms != 1 || gotGlobal.Content != "boom|repo-a" {
		t.Fatalf("global row mutated: confirms=%d content=%q", gotGlobal.Confirms, gotGlobal.Content)
	}
	if gotScoped.Confirms != 1 || gotScoped.Content != "boom" {
		t.Fatalf("scoped row wrong: confirms=%d content=%q", gotScoped.Confirms, gotScoped.Content)
	}
}

// The ID encoding must be injective even when fields contain the old
// separators ('|', ':') or NUL bytes, and scoped IDs must never equal an
// unscoped ID.
func TestV2FailureIDDomainSeparated(t *testing.T) {
	tuples := []struct{ cmd, sig, scope string }{
		{"go build", "boom", "repo-a"},
		{"go build", "boom|repo-a", ""},
		{"go build", "boom", "a|b"},
		{"go build", "boom|a", "b"},
		{"go build", "boom", "x:y"},
		{"go build", "boom:x", "y"},
		{"go build", "boom\x00x", "y"},
		{"go build", "boom", "x\x00y"},
		{"go build", "boom", ""},
		{"go build", "boom", "repo-b"},
	}
	seen := make(map[string]int, len(tuples))
	for i, tu := range tuples {
		id := failureBlockID(tu.cmd, tu.sig, tu.scope)
		if j, dup := seen[id]; dup {
			t.Fatalf("ID collision: tuple %d %q and tuple %d %q both hash to %s",
				i, tu, j, tuples[j], id)
		}
		seen[id] = i
	}
}

// v3 repair, finding 5: NEW unscoped writes use the length-prefixed encoding,
// not the injectable legacy formula. (This replaces the v2 test that pinned
// the legacy formula for new writes — finding 5 explicitly changes that
// contract; the legacy formula survives only as a read fallback, tested in
// TestV3FailureLegacyIDRowStillFound below.)
func TestV3FailureUnscopedIDNewEncoding(t *testing.T) {
	sig := NormalizeErrorSignature("boom")
	for _, cmd := range []string{"go build", "Go  Build ./...", "go test ./pkg/x"} {
		got := failureBlockID(cmd, sig, "")
		if got == v2LegacyFailureID(cmd, sig) {
			t.Fatalf("failureBlockID(%q, sig, \"\") still uses the injectable legacy formula (%s)", cmd, got)
		}
		if got != failureBlockID(cmd, sig, "") {
			t.Fatalf("failureBlockID(%q, sig, \"\") is not deterministic", cmd)
		}
	}
}

// Two different unscoped (command, signature) tuples must never share an ID,
// even when '|' makes the legacy concatenation ambiguous.
func TestV3FailureUnscopedIDInjection(t *testing.T) {
	sigA := NormalizeErrorSignature("boom|repo-a")
	sigB := NormalizeErrorSignature("repo-a")
	idA := failureBlockID("go build", sigA, "")
	idB := failureBlockID("go build|boom", sigB, "")
	if idA == idB {
		t.Fatalf("unscoped injection: (go build, boom|repo-a) and (go build|boom, repo-a) share ID %s", idA)
	}

	mgr := newFailureTestManager(t)
	a, err := mgr.RecordFailure(Failure{Command: "go build", Error: "boom|repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := mgr.RecordFailure(Failure{Command: "go build|boom", Error: "repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("recorded injections share ID %s", a.ID)
	}
	all := mustListFailures(t, mgr)
	if len(all) != 2 {
		t.Fatalf("ListFailures = %d blocks, want 2 distinct failures", len(all))
	}
	for _, f := range all {
		if f.Confirms != 1 {
			t.Fatalf("injected failure %s confirms = %d, want 1 (no cross-merge)", f.ID, f.Confirms)
		}
	}
}

// A row an older release wrote with the legacy unscoped ID must still be
// found and reinforced — not duplicated — when the same failure is recorded
// again. The row is seeded directly into the store with the old ID.
func TestV3FailureLegacyIDRowStillFound(t *testing.T) {
	mgr := newFailureTestManager(t)
	sig := NormalizeErrorSignature("boom")
	oldID := v2LegacyFailureID("go build", sig)
	old := &Block{
		ID: oldID, Tier: TierLong, Label: FailureLabel, Content: "boom", Source: "failure",
		Confirms: 1,
		Metadata: map[string]string{
			FailureMetaCommand: "go build",
			FailureMetaError:   "boom",
			FailureMetaSig:     sig,
		},
	}
	if err := mgr.Store().Add(old); err != nil {
		t.Fatal(err)
	}

	// Same failure, different command casing/whitespace: must reinforce the
	// legacy row under its OLD id.
	b, err := mgr.RecordFailure(Failure{Command: "Go  Build", Error: "boom", Fix: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != oldID {
		t.Fatalf("re-record created ID %s, want legacy %s (no duplication)", b.ID, oldID)
	}
	if b.Confirms != 2 {
		t.Fatalf("legacy row confirms = %d, want 2", b.Confirms)
	}
	all := mustListFailures(t, mgr)
	if len(all) != 1 {
		t.Fatalf("ListFailures = %d blocks, want 1 (legacy row duplicated)", len(all))
	}

	// A scoped recording of the same command+error is a DIFFERENT failure and
	// must not touch the legacy global row.
	scoped, err := mgr.RecordFailureScoped("repo-a", "", Failure{Command: "go build", Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.ID == oldID {
		t.Fatalf("scoped failure reused legacy global ID %s", oldID)
	}
	again, err := mgr.Store().Get(oldID)
	if err != nil || again.Confirms != 2 {
		t.Fatalf("legacy row mutated by scoped record: %+v (err %v)", again, err)
	}
}

// The legacy fallback must verify identity, not just the ID: the old formula
// was injectable, so a legacy row may belong to a DIFFERENT tuple. Such a row
// must not absorb the new failure, and the true sibling must still merge.
func TestV3FailureLegacyFallbackVerifiesIdentity(t *testing.T) {
	mgr := newFailureTestManager(t)
	// The old binary recorded ("go build|boom", "repo-a") but the injectable
	// formula gave it the ID of ("go build", "boom|repo-a").
	sigVictim := NormalizeErrorSignature("repo-a")
	victimID := v2LegacyFailureID("go build|boom", sigVictim)
	victim := &Block{
		ID: victimID, Tier: TierLong, Label: FailureLabel, Content: "repo-a", Source: "failure",
		Confirms: 1,
		Metadata: map[string]string{
			FailureMetaCommand: "go build|boom",
			FailureMetaError:   "repo-a",
			FailureMetaSig:     sigVictim,
		},
	}
	if err := mgr.Store().Add(victim); err != nil {
		t.Fatal(err)
	}

	// The colliding tuple must get its own row, not merge into the victim.
	other, err := mgr.RecordFailure(Failure{Command: "go build", Error: "boom|repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == victimID {
		t.Fatalf("new failure absorbed the legacy victim row %s", victimID)
	}
	if other.Confirms != 1 {
		t.Fatalf("new failure confirms = %d, want 1", other.Confirms)
	}
	gotVictim, err := mgr.Store().Get(victimID)
	if err != nil || gotVictim.Confirms != 1 || gotVictim.Content != "repo-a" {
		t.Fatalf("legacy victim row mutated: %+v (err %v)", gotVictim, err)
	}

	// The victim's TRUE sibling still finds and reinforces it.
	sib, err := mgr.RecordFailure(Failure{Command: "go build|boom", Error: "repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if sib.ID != victimID || sib.Confirms != 2 {
		t.Fatalf("true sibling = id %s confirms %d, want legacy %s / 2", sib.ID, sib.Confirms, victimID)
	}
	if all := mustListFailures(t, mgr); len(all) != 2 {
		t.Fatalf("ListFailures = %d blocks, want 2", len(all))
	}
}

// ── v2 repair, finding 2: read paths must normalize the query scope ──────────

// "repo-a", "repo-a/", "/repo-a" and whitespace variants must behave
// identically in every scoped read path, with the basename fallback active.
func TestV2FailureReadScopeNormalized(t *testing.T) {
	mgr := newFailureTestManager(t)
	recordScoped(t, mgr, "repo-a", "", Failure{
		Command: "go test ./pkg/x",
		Error:   "zz slash probe",
		Fix:     "retry",
		Paths:   []string{"pkg/main.go"},
		Symbols: []string{"github.com/org/repo-a/pkg.Alpha"},
	})
	for _, scope := range []string{"repo-a", "repo-a/", "/repo-a", " repo-a "} {
		fs, err := mgr.ListFailuresScoped(scope)
		if err != nil || len(fs) != 1 {
			t.Fatalf("ListFailuresScoped(%q) = %d blocks, err %v; want 1", scope, len(fs), err)
		}
		fs, err = mgr.FailuresForScoped(scope, "", []string{"main.go"}, nil)
		if err != nil || len(fs) != 1 {
			t.Fatalf("FailuresForScoped(%q, basename main.go) = %d hits, err %v; want 1 (basename fallback off?)", scope, len(fs), err)
		}
		fs, err = mgr.FailuresForScoped(scope, "", nil, []string{"Alpha"})
		if err != nil || len(fs) != 1 {
			t.Fatalf("FailuresForScoped(%q, symbol Alpha) = %d hits, err %v; want 1", scope, len(fs), err)
		}
		cw, err := mgr.BuildTaskContextFor(scope, "", []string{"main.go"}, nil)
		if err != nil || len(cw.Failures) != 1 {
			t.Fatalf("BuildTaskContextFor(%q, main.go) failures = %d, err %v; want 1", scope, len(cw.Failures), err)
		}
	}
	// The cross-repo basename guard must survive normalization.
	fs, err := mgr.FailuresForScoped("repo-b/", "", []string{"main.go"}, nil)
	if err != nil || len(fs) != 0 {
		t.Fatalf("repo-b/ basename query must not leak repo-a failure, got %v (err %v)", fs, err)
	}
}

// ── v3 repair, finding 1: every write path normalizes the block scope ────────

// RememberBlock and AddScopedNote must store normalized scopes (no slash or
// whitespace padding), and a sentinel scope is never recordable: it becomes a
// global block. Normalized reads must find the rows under every spelling.
func TestV3WritePathsNormalizeScope(t *testing.T) {
	mgr := newFailureTestManager(t)

	b1, err := mgr.RememberBlock(&Block{Tier: TierLong, Label: "fact", Content: "zz v3 padded remember", Scope: "repo-a/", Repo: "/repo-a/"})
	if err != nil {
		t.Fatal(err)
	}
	if b1.Scope != "repo-a" || b1.Repo != "repo-a" {
		t.Fatalf("RememberBlock stored Scope=%q Repo=%q, want repo-a/repo-a", b1.Scope, b1.Repo)
	}
	b2, err := mgr.AddScopedNote("/repo-a", TierLong, "note", "zz v3 padded note")
	if err != nil {
		t.Fatal(err)
	}
	if b2.Scope != "repo-a" {
		t.Fatalf("AddScopedNote stored Scope=%q, want repo-a", b2.Scope)
	}
	b3, err := mgr.RememberBlock(&Block{Tier: TierLong, Label: "fact", Content: "zz v3 sentinel remember", Scope: " ALL "})
	if err != nil {
		t.Fatal(err)
	}
	if b3.Scope != "" {
		t.Fatalf("sentinel scope stored as %q, want global \"\"", b3.Scope)
	}

	for _, scope := range []string{"repo-a", "repo-a/", "/repo-a", " repo-a "} {
		got, err := mgr.ListScoped(scope, TierLong)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("ListScoped(%q) = %d blocks, want 3 (2 repo-a + 1 global)", scope, len(got))
		}
	}
	// A foreign scope sees only the global sentinel block.
	got, err := mgr.ListScoped("repo-b", TierLong)
	if err != nil || len(got) != 1 || got[0].Content != "zz v3 sentinel remember" {
		t.Fatalf("repo-b view = %v (err %v), want only the global block", got, err)
	}
}

// Rows an older version stored with slash-padded (or sentinel) scopes must be
// found by normalized reads after the store is reopened: a one-time migration
// canonicalizes stored scopes. The rows are seeded directly through Store.Add
// (the raw persistence primitive), exactly as an old binary wrote them.
func TestV3LegacyPaddedScopeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")

	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	old := []*Block{
		{ID: "old-padded-a", Tier: TierLong, Label: "fact", Content: "zz old padded a", Scope: "repo-a/", Repo: "repo-a/"},
		{ID: "old-padded-b", Tier: TierLong, Label: "fact", Content: "zz old padded b", Scope: "/repo-b/"},
		{ID: "old-sentinel", Tier: TierLong, Label: "fact", Content: "zz old sentinel", Scope: "all"},
		{ID: "old-failure", Tier: TierLong, Label: FailureLabel, Content: "zz old failure", Scope: "repo-a/",
			Metadata: map[string]string{FailureMetaCommand: "go build", FailureMetaSig: "zz old failure"}},
	}
	for _, b := range old {
		if err := s1.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	// Before the migration the padded rows are orphaned from canonical reads.
	m1 := NewManager(s1)
	if got, _ := m1.ListScoped("repo-a", TierLong); len(got) != 0 {
		t.Fatalf("pre-migration ListScoped(repo-a) = %d blocks, want 0 (padded rows orphaned)", len(got))
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a genuine pre-v3 database: old binaries never wrote the
	// migration ledger key, so drop the one the seeding open recorded.
	raw, err := sql.Open("sqlite", sqlitePathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM memory_meta WHERE key = ?`, "migration:scope_normalize_v3"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: the one-time scope migration runs.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(s2)
	got, err := m2.ListScoped("repo-a", TierLong)
	if err != nil {
		t.Fatal(err)
	}
	wantContents := map[string]bool{"zz old padded a": false, "zz old sentinel": false}
	for _, b := range got {
		if _, ok := wantContents[b.Content]; ok {
			wantContents[b.Content] = true
		}
		if b.Scope != NormalizeScope(b.Scope) || b.Scope == "all" {
			t.Fatalf("migrated block %s still has non-canonical scope %q", b.ID, b.Scope)
		}
	}
	for content, seen := range wantContents {
		if !seen {
			t.Fatalf("ListScoped(repo-a) after migration missing %q (got %v)", content, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("ListScoped(repo-a) after migration = %d blocks, want 3 (repo-a block + repo-a failure + healed global), got %v", len(got), got)
	}
	gotB, _ := m2.ListScoped("repo-b", TierLong)
	if len(gotB) != 2 { // repo-b + global sentinel; never repo-a
		t.Fatalf("ListScoped(repo-b) after migration = %d blocks, want 2, got %v", len(gotB), gotB)
	}
	for _, b := range gotB {
		if b.Content == "zz old padded a" {
			t.Fatalf("repo-a block leaked into repo-b view: %v", gotB)
		}
	}
	// Stored forms are canonical now, IDs and confirms untouched.
	for id, wantScope := range map[string]string{"old-padded-a": "repo-a", "old-padded-b": "repo-b", "old-sentinel": "", "old-failure": "repo-a"} {
		b, err := m2.Store().Get(id)
		if err != nil {
			t.Fatalf("migrated block %s lost: %v", id, err)
		}
		if b.Scope != wantScope {
			t.Fatalf("block %s scope = %q, want %q", id, b.Scope, wantScope)
		}
		if b.Confirms != 0 {
			t.Fatalf("migration changed Confirms of %s to %d", id, b.Confirms)
		}
	}
	if b, _ := m2.Store().Get("old-padded-a"); b.Repo != "repo-a" {
		t.Fatalf("padded repo not migrated: %q", b.Repo)
	}
	fs, err := m2.ListFailuresScoped("repo-a")
	if err != nil || len(fs) != 1 || fs[0].ID != "old-failure" {
		t.Fatalf("ListFailuresScoped(repo-a) after migration = %v (err %v), want the old failure", fs, err)
	}
	if v, err := m2.Store().Meta("migration:scope_normalize_v3"); err != nil || v != "1" {
		t.Fatalf("migration ledger = %q (err %v), want 1", v, err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	// A second reopen is a no-op: canonical scopes stay, nothing is re-keyed.
	s3, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	m3 := NewManager(s3)
	got3, _ := m3.ListScoped("repo-a", TierLong)
	if len(got3) != 3 {
		t.Fatalf("second reopen changed the repo-a view: %v", got3)
	}
	if b, _ := m3.Store().Get("old-padded-a"); b.Scope != "repo-a" || b.ID != "old-padded-a" {
		t.Fatalf("second reopen mutated block: %+v", b)
	}
}

// Markdown import is a block-scope write path too: front-matter scopes are
// normalized on import.
func TestV3ImportNormalizesScope(t *testing.T) {
	mgr := newFailureTestManager(t)
	dir := t.TempDir()
	now := time.Now()
	b := &Block{ID: "imp-v3", Tier: TierLong, Label: "fact", Content: "zz v3 import", Scope: "repo-a/", Repo: "/repo-a/", CreatedAt: now, UpdatedAt: now}
	data, err := BlockToMarkdown(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "imp-v3.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Import(dir); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.ListScoped("repo-a", TierLong)
	if err != nil || len(got) != 1 || got[0].Scope != "repo-a" || got[0].Repo != "repo-a" {
		t.Fatalf("imported block = %v (err %v), want one normalized repo-a block", got, err)
	}
}

// ── v3 repair, finding 2: a sentinel repo must not create a literal "all" scope ──

func TestV3FailureRepoSentinelNotAScope(t *testing.T) {
	mgr := newFailureTestManager(t)

	for i, repo := range []string{"all", " ALL ", "*", "/all/"} {
		b, err := mgr.RecordFailureScoped("", repo, Failure{Command: "go build", Error: fmt.Sprintf("zz v3 repo sentinel %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if b.Scope != "" {
			t.Fatalf("repo %q created scope %q, want global \"\"", repo, b.Scope)
		}
		if IsAllScopesSentinel(b.Repo) {
			t.Fatalf("repo %q stored as Repo=%q, want sanitized", repo, b.Repo)
		}
	}

	// A padded repo still defaults the scope (normalized).
	b, err := mgr.RecordFailureScoped("", "/repo-b/", Failure{Command: "go test", Error: "zz v3 repo padded"})
	if err != nil || b.Scope != "repo-b" || b.Repo != "repo-b" {
		t.Fatalf("padded repo default = scope %q repo %q (err %v), want repo-b/repo-b", b.Scope, b.Repo, err)
	}

	// An explicit scope survives a sentinel repo; the repo is sanitized.
	b, err = mgr.RecordFailureScoped("repo-a", "all", Failure{Command: "go vet", Error: "zz v3 scope kept"})
	if err != nil || b.Scope != "repo-a" || b.Repo != "" {
		t.Fatalf("explicit scope + sentinel repo = scope %q repo %q (err %v), want repo-a/\"\"", b.Scope, b.Repo, err)
	}

	// The sentinel-repo failures are global: visible from any scope, and no
	// stored failure carries the literal "all" scope.
	fs, err := mgr.ListFailuresScoped("repo-a")
	if err != nil || len(fs) != 5 { // 4 global + 1 repo-a; never repo-b
		t.Fatalf("ListFailuresScoped(repo-a) = %d failures (err %v), want 5", len(fs), err)
	}
	for _, b := range mustListFailures(t, mgr) {
		if b.Scope == "all" {
			t.Fatalf("failure %s stored with literal scope \"all\"", b.ID)
		}
	}
}
