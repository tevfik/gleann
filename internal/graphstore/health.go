package graphstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/store"
)

// GraphExtractorVersion is the format of the graph rows this binary produces:
// bump it whenever the extractors or the resolver change what they emit, so a
// graph written by an older gleann is recognisably stale (v1.22 T3). Nothing
// migrates the rows automatically — the answer is `gleann graph build <name>`,
// which every consumer of a stale record prints.
const GraphExtractorVersion = "1"

// Resolver modes recorded in store.MetaGraphResolver.
const (
	// ResolverExact means the Go files of the graph were resolved with
	// go/types: calls through a receiver, a variable or an interface are
	// bound to their declaration.
	ResolverExact = "exact"
	// ResolverSyntactic means the Go files were resolved by name matching
	// only: such calls are missing from the graph. store.MetaGraphResolverReason
	// says why (no go.mod, GLEANN_GRAPH_EXACT=0, …).
	ResolverSyntactic = "syntactic"
)

// GraphHealth is how a code graph was built (v1.22 T3). A graph carries this
// record in the meta table of its own database, so `graph stats`, `index info`,
// `gleann doctor` and the MCP graph tools can tell a fresh graph from one left
// behind by an older gleann or built without a resolver.
//
// Every field is descriptive: a missing key reads as the empty string and
// marks the record Stale, it never makes a graph unreadable.
type GraphHealth struct {
	// ExtractorVersion is the GraphExtractorVersion of the binary that built
	// the graph ("" = built before the record existed).
	ExtractorVersion string `json:"extractor_version,omitempty"`
	// Resolver is ResolverExact or ResolverSyntactic ("" = unknown).
	Resolver string `json:"resolver,omitempty"`
	// ResolverReason explains a syntactic resolver ("no go.mod",
	// "GLEANN_GRAPH_EXACT=0"); empty for an exact one or when unknown.
	ResolverReason string `json:"resolver_reason,omitempty"`
	// Treesitter is "1" when the graph holds tree-sitter-extracted (non-Go)
	// symbols and "0" when it does not. It mirrors the graph_treesitter key
	// that drives the lite-build notices, so the two can never disagree.
	Treesitter string `json:"treesitter,omitempty"`
	// BuiltAt is the RFC3339 UTC timestamp of the last full graph build. An
	// incremental sync preserves it untouched: it names the build that wrote
	// the rows, not the latest sync. Staleness is judged by ExtractorVersion,
	// never by this timestamp.
	BuiltAt string `json:"built_at,omitempty"`
	// Stale is true when the stored extractor version is missing or older
	// than this binary's GraphExtractorVersion: the graph is worth rebuilding.
	Stale bool `json:"stale,omitempty"`
	// DirtyFiles counts the files still marked graph_dirty: references the
	// graph could not resolve yet, retried by the next sync.
	DirtyFiles int `json:"dirty_files,omitempty"`
	// EdgesByConfidence breaks the edge counts down by kind and confidence
	// label ("extracted", "inferred", "ambiguous"), sorted by both.
	EdgesByConfidence []EdgeConfidence `json:"edges_by_confidence,omitempty"`
}

// EdgeConfidence is one (kind, confidence) edge count of GraphHealth.
type EdgeConfidence struct {
	Kind       string `json:"kind"`
	Confidence string `json:"confidence"`
	Count      int    `json:"count"`
}

// GraphHealthMeta returns the meta mutations a full graph build writes with
// its rows: the extractor version of this binary, the resolver mode (and, when
// it is syntactic, why) and the build timestamp. Pass it to ReplaceAllWithMeta
// so the record lands in the same transaction as the graph rows.
func GraphHealthMeta(resolver, reason string) map[string]*string {
	built := time.Now().UTC().Format(time.RFC3339)
	version := GraphExtractorVersion
	mode, why := resolver, reason
	if mode == "" {
		mode = ResolverSyntactic
	}
	out := map[string]*string{
		store.MetaGraphExtractorVersion: &version,
		store.MetaGraphResolver:         &mode,
		store.MetaGraphBuiltAt:          &built,
	}
	if why == "" {
		out[store.MetaGraphResolverReason] = nil // an exact resolver leaves no reason behind
	} else {
		out[store.MetaGraphResolverReason] = &why
	}
	return out
}

// MarkMetaOnce records key with "1" the first time it is seen and reports
// whether this call was the first one. The "no Go module" warning uses it so an
// index warns once instead of on every sync (v1.22 E4); the flag is cleared
// when the condition goes away (a go.mod appears) or the graph is rebuilt.
// A nil receiver answers true, so a caller without a store still warns.
func (g *SQLite) MarkMetaOnce(ctx context.Context, key string) (bool, error) {
	if g == nil {
		return true, nil
	}
	res, err := g.writer.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, '1') ON CONFLICT(key) DO NOTHING`, key)
	if err != nil {
		return false, fmt.Errorf("marking meta %s: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("marking meta %s: %w", key, err)
	}
	return n > 0, nil
}

// graphMetaKey reads one meta value; ok is false when the key is absent.
func (g *SQLite) graphMetaKey(ctx context.Context, key string) (string, bool) {
	var v string
	if err := g.reader.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

// GraphHealth reads the graph health record. A graph written before the record
// existed answers with an empty, Stale record rather than an error, so callers
// can print the rebuild hint without special-casing old indexes.
func (g *SQLite) GraphHealth() (*GraphHealth, error) {
	ctx := readCtx()
	h := &GraphHealth{}
	h.ExtractorVersion, _ = g.graphMetaKey(ctx, store.MetaGraphExtractorVersion)
	h.Resolver, _ = g.graphMetaKey(ctx, store.MetaGraphResolver)
	h.ResolverReason, _ = g.graphMetaKey(ctx, store.MetaGraphResolverReason)
	h.BuiltAt, _ = g.graphMetaKey(ctx, store.MetaGraphBuiltAt)
	h.Stale = versionStale(h.ExtractorVersion)

	// The tree-sitter flag follows the key the lite notices already use; a
	// graph without the key (or written before it) is judged by its rows.
	h.Treesitter = "0"
	if g.HasTreeSitterEdges() {
		h.Treesitter = "1"
	}

	if err := g.reader.QueryRowContext(ctx, `SELECT count(*) FROM files WHERE graph_dirty = 1`).Scan(&h.DirtyFiles); err != nil {
		return nil, fmt.Errorf("counting dirty graph files: %w", err)
	}

	rows, err := g.reader.QueryContext(ctx, `
SELECT k.label, c.label, count(*)
FROM graph_edges e
JOIN graph_labels k ON k.id = e.kind
JOIN graph_labels c ON c.id = e.confidence
GROUP BY k.label, c.label
ORDER BY k.label, c.label`)
	if err != nil {
		return nil, fmt.Errorf("counting graph edges by confidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e EdgeConfidence
		if err := rows.Scan(&e.Kind, &e.Confidence, &e.Count); err != nil {
			return nil, fmt.Errorf("counting graph edges by confidence: %w", err)
		}
		h.EdgesByConfidence = append(h.EdgesByConfidence, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("counting graph edges by confidence: %w", err)
	}
	return h, nil
}

// RecordGraphHealth writes the graph health metadata keys in their own
// transaction. A full graph build passes GraphHealthMeta into its single write
// transaction instead; this is for callers that only need the record (tests,
// repair paths, tooling).
func (g *SQLite) RecordGraphHealth(ctx context.Context, resolver, reason string) error {
	tx, err := g.writer.BeginTx(ctx, nil) // _txlock=immediate: BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("recording graph health: beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if err := g.applyMetaMutationsTx(ctx, tx, GraphHealthMeta(resolver, reason)); err != nil {
		return fmt.Errorf("recording graph health: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recording graph health: committing: %w", err)
	}
	return nil
}

// versionStale reports whether a graph built by storedVersion needs a rebuild
// against this binary. A missing version (a graph predating the record) is
// stale; so is a numerically older one; an unparseable one is stale unless it
// matches exactly.
func versionStale(stored string) bool {
	if stored == "" {
		return true
	}
	if stored == GraphExtractorVersion {
		return false
	}
	want, err1 := strconv.Atoi(GraphExtractorVersion)
	got, err2 := strconv.Atoi(stored)
	if err1 != nil || err2 != nil {
		return true
	}
	return got < want
}

// HealthLines renders a health record as aligned "label: value" lines, one per
// fact, without the rebuild hint (VersionDriftHint supplies that). Values a
// pre-record graph never wrote print as "unknown"/"never recorded" instead of
// an empty string, so a consumer never reads a blank as "none".
func HealthLines(h *GraphHealth) []string {
	if h == nil {
		return nil
	}
	version := "v" + h.ExtractorVersion
	if h.ExtractorVersion == "" {
		version = "unknown (graph written before this record existed)"
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
	lines := []string{
		fmt.Sprintf("Graph extractor:     %s", version),
		fmt.Sprintf("Resolver:            %s", resolver),
		fmt.Sprintf("Tree-sitter:         %s", treesitter),
		fmt.Sprintf("Built at:            %s", builtAt),
		fmt.Sprintf("Dirty files:         %d", h.DirtyFiles),
	}
	if parts := ConfidenceSummary(h.EdgesByConfidence); parts != "" {
		lines = append(lines, fmt.Sprintf("Edges by confidence: %s", parts))
	}
	return lines
}

// ConfidenceSummary renders edge counts by confidence as
// "CALLS exact=61, CALLS extracted=13, REFERENCES extracted=4".
func ConfidenceSummary(byConf []EdgeConfidence) string {
	parts := make([]string, 0, len(byConf))
	for _, e := range byConf {
		conf := e.Confidence
		if conf == "" {
			conf = "unspecified"
		}
		parts = append(parts, fmt.Sprintf("%s %s=%d", e.Kind, conf, e.Count))
	}
	return strings.Join(parts, ", ")
}

// VersionDriftHint is the actionable line every consumer prints for a stale
// graph: the stored version, this binary's version, and the command that fixes
// it. It returns "" for a graph that needs no rebuild.
func VersionDriftHint(indexName string, h *GraphHealth) string {
	if h == nil || !h.Stale {
		return ""
	}
	stored := h.ExtractorVersion
	if stored == "" {
		return fmt.Sprintf("code graph was built by an unknown graph extractor version, this gleann writes v%s — rebuild it with: gleann graph build %s",
			GraphExtractorVersion, indexName)
	}
	return fmt.Sprintf("code graph was built by graph extractor v%s, this gleann writes v%s — rebuild it with: gleann graph build %s",
		stored, GraphExtractorVersion, indexName)
}
