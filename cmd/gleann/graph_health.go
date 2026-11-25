package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/pkg/gleann"
)

// cmdGraphStats implements `gleann graph stats --index <name> [--json]`: the
// graph's row counts and its health record (v1.22 T3) — which graph extractor
// wrote it, whether Go calls were resolved exactly or syntactically, whether
// tree-sitter contributed, when the graph was last built in full, how many
// files are still dirty and how the edges break down by confidence. A graph
// written by an older gleann also gets the rebuild hint.
func cmdGraphStats(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann graph stats --index <name> [--json]")
		exitFunc(1)
	}
	gs, err := gleann.OpenGraphStoreReadOnly(config.IndexDir, indexName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening the code graph of index %q: %v\n", indexName, err)
		exitFunc(1)
	}
	if gs == nil {
		fmt.Fprintf(os.Stderr, "error: index %q has no code graph; build one with: gleann index build %s --docs <dir> --graph\n", indexName, indexName)
		exitFunc(1)
	}
	defer gs.Close()

	st, err := gs.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading the graph statistics of index %q: %v\n", indexName, err)
		exitFunc(1)
	}
	// Stats() stays counters-only (point lookups): the health record costs a
	// scan of the edge table, so this health-displaying command fetches it
	// explicitly (v1.22 M1).
	h, err := gs.GraphHealth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading the graph health record of index %q: %v\n", indexName, err)
		exitFunc(1)
	}
	st.Health = h

	if hasFlag(args, "--json") {
		out, err := json.MarshalIndent(gleann.GraphStats{
			Files:           st.Files,
			Symbols:         st.Symbols,
			CallEdges:       st.CallEdges,
			DeclareEdges:    st.DeclareEdges,
			ImplementsEdges: st.ImplementsEdges,
			Health:          st.Health,
		}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: encoding graph statistics: %v\n", err)
			exitFunc(1)
		}
		fmt.Println(string(out))
		return
	}

	fmt.Printf("🕸️  Graph — %s\n\n", indexName)
	fmt.Printf("  Files:            %d\n", st.Files)
	fmt.Printf("  Symbols:          %d\n", st.Symbols)
	fmt.Printf("  Call edges:       %d\n", st.CallEdges)
	fmt.Printf("  Declare edges:    %d\n", st.DeclareEdges)
	fmt.Printf("  Implements edges: %d\n", st.ImplementsEdges)
	if lines := graphstore.HealthLines(st.Health); len(lines) > 0 {
		fmt.Printf("\n  Graph health:\n")
		for _, line := range lines {
			fmt.Printf("    %s\n", line)
		}
	}
	if hint := graphstore.VersionDriftHint(indexName, st.Health); hint != "" {
		fmt.Printf("\n  ⚠️  %s\n", hint)
	}
}

// graphHealthSummary is the one-line graph health line `gleann index info`
// prints. It returns "" for an index with no readable code graph (no graph, an
// index still being written by a sync, an unreadable database), so `index info`
// never fails over a graph it cannot open.
func graphHealthSummary(indexDir, name string) string {
	gs, err := gleann.OpenGraphStoreReadOnly(indexDir, name)
	if err != nil || gs == nil {
		return ""
	}
	defer gs.Close()
	h, err := gs.GraphHealth()
	if err != nil || h == nil {
		return ""
	}
	version := "extractor v" + h.ExtractorVersion
	if h.ExtractorVersion == "" {
		version = "extractor unknown"
	}
	resolver := h.Resolver
	if resolver == "" {
		resolver = "unknown"
	}
	if h.ResolverReason != "" {
		resolver += " (" + h.ResolverReason + ")"
	}
	treesitter := "no"
	if h.Treesitter == "1" {
		treesitter = "yes"
	}
	builtAt := h.BuiltAt
	if builtAt == "" {
		builtAt = "never recorded"
	}
	line := fmt.Sprintf("%s, resolver %s, tree-sitter %s, built %s, %d dirty file(s)",
		version, resolver, treesitter, builtAt, h.DirtyFiles)
	if hint := graphstore.VersionDriftHint(name, h); hint != "" {
		line += "\n   ⚠️  " + hint
	}
	return line
}

// warnStaleGraphs is the `gleann doctor` check for graph provenance (v1.22
// T3): an index whose code graph was written by an older graph extractor — or
// by a gleann that predates the health record — needs
// `gleann graph build <name>`, because nothing migrates graph rows on its own.
func warnStaleGraphs(indexDir string, indexes []gleann.IndexMeta, warn func(string)) {
	for _, idx := range indexes {
		if !idx.HasGraph {
			continue
		}
		gs, err := gleann.OpenGraphStoreReadOnly(indexDir, idx.Name)
		if err != nil || gs == nil {
			continue // no graph, or a sync owns the database right now
		}
		h, err := gs.GraphHealth()
		gs.Close()
		if err != nil || h == nil {
			continue
		}
		if hint := graphstore.VersionDriftHint(idx.Name, h); hint != "" {
			warn(fmt.Sprintf("Index %q: %s", idx.Name, hint))
		}
	}
}
