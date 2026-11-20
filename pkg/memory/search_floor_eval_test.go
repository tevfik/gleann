package memory

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// floorFixture is a store of realistic agent memories (conventions, decisions,
// failures, preferences, facts). Keys are stable names used by floorQueries.
var floorFixture = []struct{ key, content string }{
	{"gofmt", "Always run gofmt -w and go vet ./... before committing Go code."},
	{"commits", "Use conventional commit messages: feat:, fix:, refactor:, test:, docs:, ci:."},
	{"main-push", "Never push directly to main; the maintainer pushes and tags releases."},
	{"no-python", "The core engine must stay pure Go; no Python runtime or virtualenv in core."},
	{"cgo", "Pure Go builds with CGO_ENABLED=0; code needing CGo must have a _stub.go fallback."},
	{"sqlite-driver", "Use the pure-Go SQLite driver modernc.org/sqlite for the index database."},
	{"wal", "The index database runs in WAL mode; one writer, many readers, serialised by the index lock."},
	{"test-cap", "Wrap go test in systemd-run with MemoryMax=8G; a 54 GB hnsw test once OOM-killed the IDE."},
	{"bench-cores", "Benchmarks run on cores 16-19 only; other cores are reserved for the benchmark runner."},
	{"staticcheck", "Run staticcheck from ~/go/bin before every commit; CI fails on U1000 unused code."},
	{"errors", "Wrap errors with fmt.Errorf(\"doing x: %w\", err) and return them up the stack."},
	{"globals", "Avoid package-level mutable variables; pass configuration explicitly."},
	{"embed-model", "Prefer small CPU embedding models through bitllm.c: granite-embedding for code, multilingual-e5 for prose."},
	{"ollama-url", "The Ollama server listens on http://localhost:11434 unless OLLAMA_HOST is set."},
	{"openai-no-implicit", "Never call the OpenAI cloud implicitly; an explicit base URL is required."},
	{"bezgin-host", "The bezgin machine hosts the matrix bot and the llama.cpp fleet engine."},
	{"bezgin-cache", "On bezgin run llama-server with -ub 2048 and --no-cache-idle-slots so the prefix cache works."},
	{"matrix-tags", "Build the matrix bot with -tags goolm; fetch before editing because the local copy lags bezgin."},
	{"searxng-ip", "The SearXNG vane IP drifts; look it up again before configuring the bot search backend."},
	{"memory-poison", "The devran persona writes junk blocks into bot memory, three duplicates per scope."},
	{"kuzu", "Kuzu was removed from the graph store; keep a pluggable graph database benchmark harness instead."},
	{"graph-sqlite", "The code graph lives in the index SQLite database and is rebuilt by gleann index sync."},
	{"symbols-anchor", "Every first-party symbol node needs a source anchor; never synthesise phantom symbols from call sites."},
	{"treesitter", "Tree-sitter parsers are only built with -tags treesitter and need a pure Go stub otherwise."},
	{"release-v120", "v1.20.0 was published on 2026-11-17 with 22 release assets."},
	{"release-squash", "Before a release squash the work commits into a handful of topic commits and ask before rewriting main."},
	{"awesome-go", "The awesome-go pull request 6834 was merged; coverage was 94.3 percent."},
	{"windows-ci", "Windows CI failed on path separators in upload file names; reject empty names portably."},
	{"watch-flaky", "The watch test was flaky because file changes started before the initial sync finished."},
	{"hnsw-recall", "DiskANN recall dropped after the pruning change; the search list size was too small."},
	{"keyword-search", "Keyword search must not return stop word matches; tokenise with the shared analyzer."},
	{"rerank", "Use the cross-encoder reranker only for the final top 20 candidates to keep latency low."},
	{"chunk-size", "Code is chunked at function boundaries; prose chunks are 512 tokens with 64 token overlap."},
	{"api-port", "The REST server listens on port 8081 by default and the MCP server speaks stdio."},
	{"auth-token", "Server auth uses a bearer token from GLEANN_API_TOKEN; never log the token."},
	{"docs-style", "Documentation tables list env vars alphabetically with default and description columns."},
	{"changelog", "Every user visible change needs a CHANGELOG.md entry under Unreleased."},
	{"emoji", "Do not use emojis in code, docs or commit messages."},
	{"no-attrib", "Never add Co-Authored-By or generated-with lines to commits or pull requests."},
	{"agents-md", "AGENTS.md holds the repository rules; nested AGENTS.md files override it per directory."},
	{"plan-first", "Non trivial features start as an RFC under docs/plans before any code is written."},
	{"subagents", "Use Anthropic agents for subagent work; never route work to DeepSeek or Kilo."},
	{"budget", "Injected memory is capped at 4000 tokens; whole blocks drop lowest priority first."},
	{"contradict", "Contradiction detection marks conflicting long-term blocks instead of overwriting them."},
	{"failure-dedup", "Failure memories are deduplicated by normalised error signature plus command."},
	{"sleeptime", "The sleep-time engine extracts memories from conversations and records a change set for revert."},
	{"mcp-tools", "The MCP server exposes gleann_search, gleann_read, gleann_def and gleann_impact tools."},
	{"index-lock", "Only one build or sync per index at a time; the lock file is <name>.lock."},
	{"pdf-plugin", "PDF and OCR extraction live in the gleann-plugin-docs plugin over HTTP, never in core."},
	{"embedding-dim", "The default embedding dimension is 768; changing the model requires a full rebuild."},
	{"hybrid", "Hybrid search fuses BM25 and vector ranks with reciprocal rank fusion, k=60."},
	{"tui", "The agent chat TUI theme is chosen with GLEANN_THEME; the classic UI needs --classic."},
	{"backup", "Take a git bundle backup before rewriting history on main."},
	{"timeout", "Integration tests use a 5 minute timeout and the race detector."},
	{"coverage", "Coverage target is 90 percent; use portable tests that do not rely on unix permissions."},
	{"docker", "Docker is only an optional deployment convenience, never a build prerequisite."},
	{"arm", "Release builds cover linux, darwin and windows on amd64 and arm64."},
	{"licence", "Gleann is MIT licensed; third party model licences are listed in NOTICE."},
	{"user-lang", "The user writes Turkish in chat but wants English in code, commits and docs."},
	{"verbose-out", "The user prefers concise answers with the evidence first and no recap of the diff."},
	{"flaky-net", "Tests must not touch the network; use httptest servers and t.TempDir."},
}

// floorQueries are agent-style queries. want lists fixture keys that a good
// memory search must return; an empty want means "nothing relevant exists"
// and any hit is a false positive.
var floorQueries = []struct {
	query string
	want  []string
}{
	{"what do I run before committing go code", []string{"gofmt", "staticcheck"}},
	{"how should commit messages be formatted", []string{"commits", "emoji"}},
	{"can I push to main", []string{"main-push"}},
	{"is python allowed in the core engine", []string{"no-python"}},
	{"how to handle cgo and build tags", []string{"cgo"}},
	{"which sqlite driver do we use", []string{"sqlite-driver"}},
	{"index database locking and concurrency", []string{"wal", "index-lock"}},
	{"how do I run tests without running out of memory", []string{"test-cap"}},
	{"which cpu cores may benchmarks use", []string{"bench-cores"}},
	{"error wrapping convention", []string{"errors"}},
	{"which embedding model should I use for code", []string{"embed-model"}},
	{"where does ollama listen", []string{"ollama-url"}},
	{"llama server cache flags on bezgin", []string{"bezgin-cache"}},
	{"how to build the matrix bot", []string{"matrix-tags"}},
	{"why did kuzu go away", []string{"kuzu"}},
	{"when was v1.20.0 published", []string{"release-v120"}},
	{"squash commits before release", []string{"release-squash"}},
	{"windows ci failure with upload names", []string{"windows-ci"}},
	{"why was the watch test flaky", []string{"watch-flaky"}},
	{"diskann recall regression", []string{"hnsw-recall"}},
	{"how is memory injection limited", []string{"budget"}},
	{"how are failures deduplicated", []string{"failure-dedup"}},
	{"how do I revert a sleep time reflection", []string{"sleeptime"}},
	{"where does pdf ocr live", []string{"pdf-plugin"}},
	{"what port does the server use", []string{"api-port"}},
	{"does the user want emojis", []string{"emoji"}},
	{"how does hybrid search fuse ranks", []string{"hybrid"}},
	{"RRF", []string{"hybrid"}},
	{"should tests use the network", []string{"flaky-net"}},
	// No relevant memory exists: only loose, one-common-word matches are possible.
	{"how do I deploy the kubernetes cluster to production", nil},
	{"what is the recipe for chocolate cake", nil},
	{"which database should we use for the billing service", nil},
	{"what time zone does the user live in", nil},
	{"how to configure the nginx reverse proxy for the web server", nil},
	{"what is our policy on code review approvals", nil},
	{"how do I rotate the aws credentials", nil},
	{"why is the build slow on the ci runner", nil},
}

type floorScore struct {
	name                      string
	returned, relevant, found int
	wantTotal                 int
	missed                    []string
	noneQueries, noneFP       int // none-queries answered with at least one block
	noneBlocks                int
	noneHits                  []string
}

func evalFloor(name string, f floorOptions, blocks []Block, keyOf map[string]string) floorScore {
	sc := floorScore{name: name}
	for _, q := range floorQueries {
		got := rankBlocksWith(blocks, q.query, f)
		want := map[string]bool{}
		for _, w := range q.want {
			want[w] = true
		}
		sc.returned += len(got)
		sc.wantTotal += len(want)
		seen := map[string]bool{}
		for _, b := range got {
			k := keyOf[b.ID]
			seen[k] = true
			if want[k] {
				sc.relevant++
			}
		}
		for w := range want {
			if seen[w] {
				sc.found++
			} else {
				sc.missed = append(sc.missed, fmt.Sprintf("%q misses %s", q.query, w))
			}
		}
		if len(want) == 0 {
			sc.noneQueries++
			if len(got) > 0 {
				sc.noneFP++
				sc.noneBlocks += len(got)
				for _, b := range got {
					sc.noneHits = append(sc.noneHits, fmt.Sprintf("%q -> %s", q.query, keyOf[b.ID]))
				}
			}
		}
	}
	sort.Strings(sc.missed)
	return sc
}

func (s floorScore) String() string {
	p, r := 0.0, 0.0
	if s.returned > 0 {
		p = float64(s.relevant) / float64(s.returned)
	}
	if s.wantTotal > 0 {
		r = float64(s.found) / float64(s.wantTotal)
	}
	return fmt.Sprintf("%-26s returned=%3d relevant=%2d/%2d precision=%.3f recall=%.3f none-queries-with-hits=%d/%d (%d blocks)",
		s.name, s.returned, s.found, s.wantTotal, p, r, s.noneFP, s.noneQueries, s.noneBlocks)
}

func floorBlocks() ([]Block, map[string]string) {
	keyOf := map[string]string{}
	var blocks []Block
	base := time.Now().Add(-time.Hour)
	for i, f := range floorFixture {
		b := Block{ID: "id-" + f.key, Tier: TierLong, Label: "user_memory", Content: f.content,
			CreatedAt: base.Add(time.Duration(len(floorFixture)-i) * time.Minute)} // newest first
		keyOf[b.ID] = f.key
		blocks = append(blocks, b)
	}
	return blocks, keyOf
}

// TestSearchFloorEvaluation compares the previous any-token rule with the
// floor on a labeled fixture. It pins the shipped defaults: no relevant
// memory may be lost, and the loose matches must go. The old rule is
// floorOptions{} (floor disabled), so this test also fails on the old code
// (rankBlocksWith does not exist there; the loose-match assertions below are
// what the old rule violates).
func TestSearchFloorEvaluation(t *testing.T) {
	blocks, keyOf := floorBlocks()
	if len(floorFixture) < 40 || len(floorQueries) < 30 {
		t.Fatalf("fixture too small: %d blocks, %d queries", len(floorFixture), len(floorQueries))
	}
	variants := []struct {
		name string
		f    floorOptions
	}{
		{"old (any one token)", floorOptions{}},
		{"min(2,ceil(.35n))", floorOptions{enabled: true, coverage: 0.35, maxMatch: 2}},
		{"  + rel>=0.25 of top", floorOptions{enabled: true, coverage: 0.35, maxMatch: 2, relative: 0.25}},
		{"  + rel>=0.50 of top", floorOptions{enabled: true, coverage: 0.35, maxMatch: 2, relative: 0.5}},
		{"min(2,ceil(.5n))", floorOptions{enabled: true, coverage: 0.5, maxMatch: 2}},
		{"min(3,ceil(.5n))", floorOptions{enabled: true, coverage: 0.5, maxMatch: 3}},
	}
	scores := map[string]floorScore{}
	for _, v := range variants {
		s := evalFloor(v.name, v.f, blocks, keyOf)
		scores[v.name] = s
		t.Log(s.String())
		for _, m := range s.missed {
			t.Log("    lost: " + strings.TrimSpace(m))
		}
		if v.f.enabled {
			for _, m := range s.noneHits {
				t.Log("    loose: " + m)
			}
		}
	}

	old := scores["old (any one token)"]
	// Two labels are lexically unreachable for any token matcher ("RRF" vs
	// "reciprocal rank fusion", "wrapping" vs "wrap"). The floor must lose
	// nothing beyond that ceiling.
	if len(old.missed) != 2 {
		t.Fatalf("expected 2 lexically unreachable labels, got %v", old.missed)
	}
	def := evalFloor("shipped default", defaultFloor(), blocks, keyOf)
	t.Log(def.String())
	if len(def.missed) != len(old.missed) {
		t.Errorf("default floor lost relevant memories: %v (old rule: %v)", def.missed, old.missed)
	}
	// "which database should we use for the billing service" keeps one hit:
	// "database" and "use" both occur in the SQLite driver memory, which is a
	// lexical near-miss the token floor cannot tell from a real match.
	if def.noneFP > 1 || def.noneBlocks > 1 {
		t.Errorf("default floor still answers %d unrelated queries (%d blocks): %v", def.noneFP, def.noneBlocks, def.noneHits)
	}
	if def.returned >= old.returned {
		t.Errorf("floor removed nothing: %d -> %d returned", old.returned, def.returned)
	}
}
