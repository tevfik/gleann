package graphstore_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// healthSym is a declaration of a health fixture: a plain function unless
// isTest marks it as a test-file symbol.
func healthSym(fqn, file string, line int64, isTest bool) graphstore.Symbol {
	return graphstore.Symbol{
		FQN: fqn, Kind: "function", File: file, Name: fqn,
		StartLine: line, EndLine: line + 4, StartCol: 1, ByteOffset: line * 40,
		IsTest: isTest,
	}
}

// healthDeltas is a two-file graph: a.go declares p.A and p.B, b_test.go
// declares test symbol p.Test and owns one internal CALLS edge (extracted)
// plus one external CALLS edge (inferred).
func healthDeltas() []graphstore.FileDelta {
	return []graphstore.FileDelta{
		{
			File: "a.go", GraphHash: "a1",
			Symbols: []graphstore.Symbol{healthSym("p.A", "a.go", 1, false), healthSym("p.B", "a.go", 10, false)},
		},
		{
			File: "b_test.go", GraphHash: "b1",
			Symbols: []graphstore.Symbol{healthSym("p.Test", "b_test.go", 1, true)},
			Edges: []graphstore.Edge{
				{Src: "p.Test", Dst: "p.A", Kind: "CALLS", Confidence: "extracted"},
				{Src: "p.Test", Dst: "fmt.Println", Kind: "CALLS", DstExternal: true, Confidence: "inferred"},
			},
		},
	}
}

// metaValue reads one meta row straight from the database, so a test can tell
// "the key was never written" apart from "the key holds the empty string".
func metaValue(t *testing.T, s *store.Store, key string) (string, bool) {
	t.Helper()
	var v string
	if err := s.ReaderDB().QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

// olderVersion returns a version string strictly older than current: one below
// it when both are integers, a lexicographically smaller marker otherwise.
func olderVersion(t *testing.T, current string) string {
	t.Helper()
	n, err := strconv.Atoi(current)
	if err != nil {
		t.Fatalf("GraphExtractorVersion %q must be an integer so staleness can be ordered", current)
	}
	return strconv.Itoa(n - 1)
}

// TestGraphHealthRecordRoundTrip is the T3 red test: a full graph write must
// record how the graph was built (extractor version, resolver mode and reason,
// tree-sitter availability, build timestamp) and GraphHealth must read it back;
// Stats stays counters-only (v1.22 M1) and must not carry the record.
func TestGraphHealthRecordRoundTrip(t *testing.T) {
	g, s := openGraph(t)
	ctx := context.Background()

	if err := g.ReplaceAllWithMeta(ctx, healthDeltas(), graphstore.GraphHealthMeta(graphstore.ResolverSyntactic, "no go.mod")); err != nil {
		t.Fatalf("ReplaceAllWithMeta: %v", err)
	}

	for _, key := range []string{
		store.MetaGraphExtractorVersion, store.MetaGraphResolver,
		store.MetaGraphResolverReason, store.MetaGraphBuiltAt,
	} {
		if v, ok := metaValue(t, s, key); !ok || v == "" {
			t.Errorf("meta key %q after a full build: value %q ok=%v, want a non-empty value", key, v, ok)
		}
	}
	if v, _ := metaValue(t, s, store.MetaGraphResolver); v != graphstore.ResolverSyntactic {
		t.Errorf("graph.resolver = %q, want %q", v, graphstore.ResolverSyntactic)
	}
	if v, _ := metaValue(t, s, store.MetaGraphResolverReason); v != "no go.mod" {
		t.Errorf("graph.resolver_reason = %q, want %q", v, "no go.mod")
	}
	if v, _ := metaValue(t, s, store.MetaGraphExtractorVersion); v != graphstore.GraphExtractorVersion {
		t.Errorf("graph.extractor_version = %q, want the binary's %q", v, graphstore.GraphExtractorVersion)
	}
	if v, _ := metaValue(t, s, store.MetaGraphBuiltAt); v != "" {
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			t.Errorf("graph.built_at = %q, want an RFC3339 UTC timestamp: %v", v, err)
		}
	}

	h, err := g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth: %v", err)
	}
	if h == nil {
		t.Fatal("GraphHealth = nil after a full build, want a health record")
	}
	if h.ExtractorVersion != graphstore.GraphExtractorVersion {
		t.Errorf("health.ExtractorVersion = %q, want %q", h.ExtractorVersion, graphstore.GraphExtractorVersion)
	}
	if h.Resolver != graphstore.ResolverSyntactic || h.ResolverReason != "no go.mod" {
		t.Errorf("health.Resolver = %q (reason %q), want %q (reason %q)",
			h.Resolver, h.ResolverReason, graphstore.ResolverSyntactic, "no go.mod")
	}
	if h.Treesitter != "0" {
		t.Errorf("health.Treesitter = %q, want %q for a Go-only graph", h.Treesitter, "0")
	}
	if h.Stale {
		t.Errorf("health.Stale = true for a graph built by this binary (version %q)", h.ExtractorVersion)
	}
	if h.DirtyFiles != 0 {
		t.Errorf("health.DirtyFiles = %d, want 0", h.DirtyFiles)
	}
	// Edge counts by confidence (plan T3: "edge counts by confidence").
	if got := healthEdgeCount(h, "CALLS", "extracted"); got != 1 {
		t.Errorf("CALLS/extracted edges = %d, want 1 (edges by confidence: %v)", got, h.EdgesByConfidence)
	}
	if got := healthEdgeCount(h, "CALLS", "inferred"); got != 1 {
		t.Errorf("CALLS/inferred edges = %d, want 1 (edges by confidence: %v)", got, h.EdgesByConfidence)
	}

	st, err := g.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Health != nil {
		t.Errorf("Stats().Health = %+v, want nil: Stats stays counters-only (the record above came from GraphHealth)", st.Health)
	}
}

func healthEdgeCount(h *graphstore.GraphHealth, kind, confidence string) int {
	for _, e := range h.EdgesByConfidence {
		if e.Kind == kind && e.Confidence == confidence {
			return e.Count
		}
	}
	return 0
}

// TestGraphHealthSurvivesApplyFileDelta is the T3 red test for incremental
// sync: ApplyFileDelta must never drop graph.extractor_version or
// graph.built_at (or the resolver record).
func TestGraphHealthSurvivesApplyFileDelta(t *testing.T) {
	g, s := openGraph(t)
	ctx := context.Background()

	if err := g.ReplaceAllWithMeta(ctx, healthDeltas(), graphstore.GraphHealthMeta(graphstore.ResolverExact, "")); err != nil {
		t.Fatalf("ReplaceAllWithMeta: %v", err)
	}
	before, err := g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth: %v", err)
	}

	// A plain incremental delta (no metadata of its own) must leave the record
	// intact, and a delta that carries unrelated metadata too.
	delta := graphstore.FileDelta{
		File: "a.go", GraphHash: "a2",
		Symbols: []graphstore.Symbol{healthSym("p.A", "a.go", 1, false), healthSym("p.B", "a.go", 12, false), healthSym("p.New", "a.go", 20, false)},
	}
	if err := g.ApplyFileDelta(ctx, delta); err != nil {
		t.Fatalf("ApplyFileDelta: %v", err)
	}
	if err := g.ApplyFileDeltaWithMeta(ctx, delta, map[string]*string{"graph_nongo_lite:a.go": strPtr("1")}); err != nil {
		t.Fatalf("ApplyFileDeltaWithMeta: %v", err)
	}
	if err := g.RemoveFile(ctx, "b_test.go"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}

	after, err := g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth after sync: %v", err)
	}
	if after == nil {
		t.Fatal("GraphHealth = nil after ApplyFileDelta, want the record preserved")
	}
	if after.ExtractorVersion != before.ExtractorVersion || after.ExtractorVersion != graphstore.GraphExtractorVersion {
		t.Errorf("extractor version after sync = %q, want %q preserved", after.ExtractorVersion, before.ExtractorVersion)
	}
	if after.Resolver != graphstore.ResolverExact {
		t.Errorf("resolver after sync = %q, want %q preserved", after.Resolver, graphstore.ResolverExact)
	}
	if after.BuiltAt != before.BuiltAt {
		t.Errorf("built_at after sync = %q, want the recorded %q (a sync must not rewrite the full-build timestamp)", after.BuiltAt, before.BuiltAt)
	}
	if v, ok := metaValue(t, s, store.MetaGraphBuiltAt); !ok || v != before.BuiltAt {
		t.Errorf("meta graph.built_at after sync = %q (ok=%v), want %q", v, ok, before.BuiltAt)
	}
	if after.Stale {
		t.Error("health.Stale = true after a sync of a graph this binary built")
	}
}

// TestGraphHealthOfLegacyGraph is the T3 red test for graphs written before the
// health record existed: the record must read as missing and be stale, and the
// hint must name `gleann graph build`.
func TestGraphHealthOfLegacyGraph(t *testing.T) {
	g, _ := openGraph(t)
	ctx := context.Background()

	if err := g.ReplaceAll(ctx, healthDeltas()); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	h, err := g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth: %v", err)
	}
	if h == nil {
		t.Fatal("GraphHealth = nil for a graph without a health record, want a record marked stale")
	}
	if h.ExtractorVersion != "" {
		t.Errorf("ExtractorVersion = %q for a legacy graph, want it empty", h.ExtractorVersion)
	}
	if !h.Stale {
		t.Errorf("Stale = false for a graph with no %q key", store.MetaGraphExtractorVersion)
	}
	hint := graphstore.VersionDriftHint("myidx", h)
	if !strings.Contains(hint, "gleann graph build myidx") {
		t.Errorf("VersionDriftHint = %q, want it to tell the user to run 'gleann graph build myidx'", hint)
	}
	st, err := g.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Health != nil {
		t.Errorf("Stats().Health = %+v, want nil: the stale record above comes from GraphHealth, not the counters path", st.Health)
	}
}

// TestGraphHealthOlderExtractorVersionIsStale is the T3 red test for a graph
// written by an older gleann: the version drift must be reported and hinted.
func TestGraphHealthOlderExtractorVersionIsStale(t *testing.T) {
	g, _ := openGraph(t)
	ctx := context.Background()

	old := olderVersion(t, graphstore.GraphExtractorVersion)
	meta := graphstore.GraphHealthMeta(graphstore.ResolverExact, "")
	meta[store.MetaGraphExtractorVersion] = &old
	if err := g.ReplaceAllWithMeta(ctx, healthDeltas(), meta); err != nil {
		t.Fatalf("ReplaceAllWithMeta: %v", err)
	}

	h, err := g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth: %v", err)
	}
	if h.ExtractorVersion != old {
		t.Errorf("ExtractorVersion = %q, want the stored %q", h.ExtractorVersion, old)
	}
	if !h.Stale {
		t.Errorf("Stale = false for extractor version %q, older than the binary's %q", old, graphstore.GraphExtractorVersion)
	}
	hint := graphstore.VersionDriftHint("docs", h)
	if !strings.Contains(hint, "gleann graph build docs") {
		t.Errorf("VersionDriftHint = %q, want the 'gleann graph build docs' hint", hint)
	}
	if !strings.Contains(hint, old) || !strings.Contains(hint, graphstore.GraphExtractorVersion) {
		t.Errorf("VersionDriftHint = %q, want it to name both the stored %q and the binary's %q version",
			hint, old, graphstore.GraphExtractorVersion)
	}

	// A matching version must not be reported stale.
	if err := g.SetMeta(ctx, store.MetaGraphExtractorVersion, graphstore.GraphExtractorVersion); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	h, err = g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth: %v", err)
	}
	if h.Stale {
		t.Errorf("Stale = true for the current extractor version %q", graphstore.GraphExtractorVersion)
	}
	if hint := graphstore.VersionDriftHint("docs", h); hint != "" {
		t.Errorf("VersionDriftHint for an up-to-date graph = %q, want no hint", hint)
	}
}

// TestGraphHealthDirtyFileCount exposes how many files are still marked
// graph_dirty, the count E4's "Re-applying N dirty graph file(s)" is about.
func TestGraphHealthDirtyFileCount(t *testing.T) {
	g, _ := openGraph(t)
	ctx := context.Background()

	if err := g.ReplaceAllWithMeta(ctx, healthDeltas(), graphstore.GraphHealthMeta(graphstore.ResolverSyntactic, "no go.mod")); err != nil {
		t.Fatalf("ReplaceAllWithMeta: %v", err)
	}
	if err := g.MarkFileDirty(ctx, "a.go"); err != nil {
		t.Fatalf("MarkFileDirty: %v", err)
	}
	h, err := g.GraphHealth()
	if err != nil {
		t.Fatalf("GraphHealth: %v", err)
	}
	if h.DirtyFiles != 1 {
		t.Errorf("DirtyFiles = %d, want 1", h.DirtyFiles)
	}
	st, err := g.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Health != nil {
		t.Errorf("Stats().Health = %+v, want nil; GraphHealth above reported DirtyFiles 1", st.Health)
	}
}

func strPtr(s string) *string { return &s }
