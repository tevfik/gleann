package doccheck

// This file guards the memory documentation against describing the pre-v1.20
// architecture as if it still existed. Until v1.20 the memory blocks lived in
// a bbolt file that only one process could hold open (a `gleann serve` daemon
// was auto-spawned so that other processes could route over REST), the code
// graph lived in Kùzu and `community.LoadGraphFromKuzu` loaded it into memory.
// RFC 004 replaced all of that with SQLite: `memory.sqlite` for blocks and
// entities, the index's `<name>.db` for the code graph.
//
// The roadmap that planned the bbolt/daemon era is still useful history, so it
// is archived under docs/releases/ with a notice. What must not come back is an
// *active* document (docs/*.md and README.md) that presents the removed
// mechanisms as the current design. Historical and upgrade sections are exempt:
// keeping the "Upgrading entities from v1.9" instructions is the point of the
// exemption, and so is the `bbolt` mention in `gleann index migrate` and the
// benchmark harness that compares a removed Kùzu against the SQLite graph.
//
// The exemption rules are deliberately narrow. A heading only opens a legacy
// section when it names an upgrade path or history ("Upgrading entities from
// v1.9", "Historical notes"), never for words such as "Migration" or
// "Comparison" that active docs use for current material. A line is only
// exempt when it explicitly frames the removed mechanism as gone, old, or
// benchmarked; prose that says "we upgraded the store: every process opens
// bbolt directly" is a violation, not an exemption.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// maxHeadingLevel is the deepest ATX heading (`######`) markdown allows.
const maxHeadingLevel = 6

// legacySectionHeading matches a heading that puts everything below it out of
// scope: an upgrade path or an explicitly historical section. Broad words such
// as "migration" or "comparison" are intentionally absent: docs/models.md
// ("Embedding Model Consistency & Migration", "Migrating an Index to a New
// Model") and docs/architecture.md ("Architecture Comparison") are current
// documents, and exempting their sections would hide violations.
var legacySectionHeading = regexp.MustCompile(`(?i)\b(` +
	`upgrading (?:from|entities)|migrating from|` +
	`historical|legacy|archiv(?:e|ed)|deprecated|` +
	`removed (?:backends?|features?|architecture)|former (?:design|architecture)|` +
	`pre-v1\.20` +
	`)\b`)

// legacyLineMarker matches a line that explicitly frames the removed mechanism
// as history: a removal, a replacement, a former design, an older release, or
// a v1.9 upgrade/benchmark entry. It deliberately does not match bare
// "upgrade(d)", "migration", or "comparison", because a line in an active
// section that says "We upgraded the memory store: every process opens bbolt
// directly." is a violation, not an exemption.
var legacyLineMarker = regexp.MustCompile(`(?i)(` +
	`\b(?:was|were|is|are|has been|have been)\s+(?:removed|replaced|dropped|superseded|deleted)\b|` +
	`\bno longer\s+(?:uses?|used|needed|required|exists?|supported|ships?)\b|` +
	`\bremoved (?:from|in)\b|` +
	`\bremoval of\b|` +
	`\bolder releases\b|` +
	`\bformer(?:ly)?\b|` +
	`\bhistorical(?:ly)?\b|` +
	`\blegacy\b|` +
	`\bdeprecated\b|` +
	`\bsuperseded\b|` +
	`\bpre-v1\.20\b|` +
	`\b(?:was|were) kept\b|` +
	`\bcarried over\b|` +
	`\bhad no (?:windows )?build\b|` +
	`\bupgrade path\b|` +
	`\bv1\.9[^\n]*\bk[uù]zu(?:db)?\b|\bk[uù]zu(?:db)?\b[^\n]*\bv1\.9\b|` +
	`\bbench-graphdb\b|` +
	`GRAPHDB_TAGS\s*=\s*k[uù]zu|` +
	`decisions/kuzu-to-sqlite\.md` +
	`)`)

// legacyMemoryToken is one piece of the removed architecture. Every hit in an
// active document that is not inside a historical/upgrade section is a failure.
type legacyMemoryToken struct {
	name    string
	pattern *regexp.Regexp
	// fix names the current replacement, so the failure message is actionable.
	fix string
}

var legacyMemoryTokens = []legacyMemoryToken{
	{
		name:    "LoadGraphFromKuzu",
		pattern: regexp.MustCompile(`LoadGraphFromKuzu`),
		fix:     "the code graph is SQLite now: internal/graph/community/graphstore_loader.go loads it from <index>/<name>.db",
	},
	{
		name:    "BBolt memory promotion",
		pattern: regexp.MustCompile(`(?i)\bbbolt\b`),
		fix:     "memory blocks are promoted inside memory.sqlite; bbolt is only for `gleann index migrate` and the vault tracker",
	},
	{
		name:    "memory daemon auto-spawn",
		pattern: regexp.MustCompile(`EnsureDaemon|spawnDaemonCmd|auto-spawn`),
		fix:     "no daemon is required any more: every process opens memory.sqlite (one writer, WAL) directly",
	},
	{
		name:    "internal/graph/kuzu",
		pattern: regexp.MustCompile(`internal/graph/kuzu`),
		fix:     "Kùzu was removed in v1.20 and the package no longer exists; the graph store is internal/graphstore",
	},
	{
		// Kùzu is the removed graph backend; without this token a doc could
		// keep calling it the current store as long as it avoids the old
		// package path.
		name:    "Kùzu graph backend",
		pattern: regexp.MustCompile(`(?i)\bk[uù]zu(?:db)?\b`),
		fix:     "Kùzu was removed in v1.20 (RFC 004); code graphs live in the index's SQLite database, internal/graphstore",
	},
}

// atxHeading reports whether line is an ATX markdown heading and returns its
// depth (1..6) and text without the leading '#'s and whitespace.
func atxHeading(line string) (level int, text string, ok bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	i := 0
	for i < len(line) && line[i] == '#' && i < maxHeadingLevel {
		i++
	}
	rest := line[i:]
	// A heading needs a space (or end of line) after the hashes, so "#hashtag"
	// and "####### too deep" are not headings.
	if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		return 0, "", false
	}
	return i, strings.TrimSpace(rest), true
}

// memoryBannerPhrases are the facts the archive notice must state. They are
// checked case-insensitively and in the file's head, before the old title.
var memoryBannerPhrases = []string{
	"historical archive",
	"2026-09-23",
	"pre-v1.20",
	"superseded",
	"v1.20",
	"docs/plans/003-storage.md",
	"004-v1.20-sqlite-only.md",
	"docs/memory_engine.md",
	"bbolt",
	"kùzu",
	"daemon",
}

func TestMemoryRoadmapIsArchived(t *testing.T) {
	root := repoRoot(t)

	// The top-level file described the pre-v1.20 design and marked every item
	// done; it must not stay where readers take it for current plans.
	active := filepath.Join(root, "docs", "ROADMAP-agent-memory.md")
	if _, err := os.Stat(active); err == nil {
		t.Errorf("docs/ROADMAP-agent-memory.md still exists at the top level of docs/: it documents the removed bbolt/Kùzu/daemon architecture (and every item is ticked [x]) and belongs in docs/releases/ behind a historical notice")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", active, err)
	}

	archived := filepath.Join(root, "docs", "releases", "ROADMAP-agent-memory.md")
	b, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("docs/releases/ROADMAP-agent-memory.md: %v (the roadmap must be moved here, not deleted)", err)
	}
	text := string(b)
	head := text
	if i := strings.Index(text, "\n# "); i >= 0 {
		head = text[:i] // everything above the original document title
	}
	lower := strings.ToLower(head)
	for _, phrase := range memoryBannerPhrases {
		if !strings.Contains(lower, strings.ToLower(phrase)) {
			t.Errorf("docs/releases/ROADMAP-agent-memory.md: the notice above the document title does not mention %q", phrase)
		}
	}
	if !strings.Contains(head, "##") && !strings.Contains(head, "> ") {
		t.Errorf("docs/releases/ROADMAP-agent-memory.md: the notice is not a visible markdown banner (expected a blockquote or a section before the original title)")
	}
}

// activeDocFiles lists the documents a reader treats as current: docs/*.md
// (one level, so docs/releases/ and docs/plans/ are out of scope: those are the
// places history and proposals may live) plus the README.
func activeDocFiles(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatalf("globbing docs/*.md: %v", err)
	}
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); err != nil {
		t.Fatalf("stat %s: %v", readme, err)
	}
	paths = append(paths, readme)
	if len(paths) < 20 {
		t.Fatalf("found only %d active docs; the scanner is broken", len(paths))
	}
	sort.Strings(paths)
	return paths
}

// legacyHitsInActiveDoc returns file:line references for removed-architecture
// tokens that appear outside a historical/upgrade section and outside a line
// that itself frames them as history.
func legacyHitsInActiveDoc(t *testing.T, path string) []string {
	t.Helper()
	return legacyHitsInText(filepath.ToSlash(path), readFile(t, path))
}

// legacyHitsInText is the scanner behind legacyHitsInActiveDoc, split out so
// the rules can be tested against synthetic markdown without touching the real
// docs. name is used verbatim in the reported locations.
func legacyHitsInText(name, text string) []string {
	var hits []string
	legacyLevel := 0 // heading depth of the enclosing legacy section; 0 outside
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		// A heading sets the section context; code blocks never do.
		if !inFence {
			if level, heading, ok := atxHeading(trimmed); ok {
				switch {
				case legacySectionHeading.MatchString(heading):
					// Enter (or re-enter, at a new depth) a historical section.
					legacyLevel = level
				case legacyLevel > 0 && level <= legacyLevel:
					// A heading at the same or a shallower depth closes it.
					legacyLevel = 0
				}
				// Deeper subheadings (level > legacyLevel) keep the section
				// open: "### Verification Steps" inside "## Upgrading from
				// v1.9" is still part of the upgrade instructions.
				continue
			}
		}
		if legacyLevel > 0 || legacyLineMarker.MatchString(trimmed) {
			continue
		}
		for _, tok := range legacyMemoryTokens {
			if !tok.pattern.MatchString(line) {
				continue
			}
			hits = append(hits, name+":"+strconv.Itoa(i+1)+": describes "+tok.name+" as current architecture; "+tok.fix)
			break // one finding per line is enough
		}
	}
	return hits
}

func TestNoActiveDocDescribesRemovedMemoryArchitecture(t *testing.T) {
	root := repoRoot(t)

	// Per RFC 004 the Kùzu-backed graph is gone; if this ever fails, the docs
	// rule below is wrong, not the docs.
	if _, err := os.Stat(filepath.Join(root, "internal", "graph", "kuzu")); err == nil {
		t.Fatalf("internal/graph/kuzu exists again: revisit the doc rule instead of deleting this test")
	}

	var failures []string
	for _, path := range activeDocFiles(t, root) {
		failures = append(failures, legacyHitsInActiveDoc(t, path)...)
	}
	sort.Strings(failures)
	if len(failures) > 0 {
		t.Errorf("active docs (docs/*.md, README.md) still describe the removed pre-v1.20 memory architecture:\n  %s\n\nHistorical and upgrade sections are exempt; move the text to docs/releases/ or an explicit \"Upgrading from ...\" section.",
			strings.Join(failures, "\n  "))
	}
}

// memoryUpgradeHeading / memoryUpgradeTitle identify the one section that must
// keep the one-shot v1.9 Kùzu export/import instructions.
const (
	memoryUpgradeHeading = "## Upgrading entities from v1.9"
	memoryUpgradeTitle   = "Upgrading entities from v1.9"
)

// sectionByTitle returns the body of the markdown section whose heading text
// equals title: everything after that heading up to the next heading of the
// same or shallower depth (or EOF). Headings inside fenced code blocks are
// ignored.
func sectionByTitle(text, title string) (string, bool) {
	lines := strings.Split(text, "\n")
	start, depth := -1, 0
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		level, heading, ok := atxHeading(trimmed)
		if !ok {
			continue
		}
		if start < 0 {
			if strings.EqualFold(heading, title) {
				start, depth = i, level
			}
			continue
		}
		if level <= depth {
			return strings.Join(lines[start+1:i], "\n"), true
		}
	}
	if start < 0 {
		return "", false
	}
	return strings.Join(lines[start+1:], "\n"), true
}

// TestMemoryEngineDocKeepsCurrentStoreAndV19Upgrade is the counterweight to the
// rule above: docs/memory_engine.md must describe memory.sqlite as the store
// (not a removed backend) and must keep the v1.9 entity import instructions in
// the upgrade section itself. A future "cleanup" that strips the upgrade
// section breaks users with old Kùzu stores, so the section is asserted to
// exist; a command that only survives elsewhere in the file does not count.
func TestMemoryEngineDocKeepsCurrentStoreAndV19Upgrade(t *testing.T) {
	root := repoRoot(t)
	text := readFile(t, filepath.Join(root, "docs", "memory_engine.md"))

	if !strings.Contains(text, "memory.sqlite") {
		t.Errorf("docs/memory_engine.md no longer documents memory.sqlite as the store")
	}
	body, ok := sectionByTitle(text, memoryUpgradeTitle)
	if !ok {
		t.Fatalf("docs/memory_engine.md lost its %q section: the one-shot export/import of old Kùzu entity stores is the supported upgrade path and must stay documented", memoryUpgradeHeading)
	}
	if !strings.Contains(body, "gleann memory import-graph") {
		t.Errorf("docs/memory_engine.md: the %q section no longer shows `gleann memory import-graph` (a mention elsewhere in the file does not count)", memoryUpgradeHeading)
	}
}

// TestSectionByTitleStopsAtSameLevel pins the scope rule used above: the body
// must include deeper subsections but no text from a following sibling section.
func TestSectionByTitleStopsAtSameLevel(t *testing.T) {
	doc := strings.Join([]string{
		"# Memory",
		"",
		"## Storage",
		"",
		"`gleann memory import-graph` appears before the upgrade section.",
		"",
		memoryUpgradeHeading,
		"",
		"Run `gleann memory import-graph` here.",
		"",
		"### Verification Steps",
		"",
		"Still part of the upgrade section.",
		"",
		"## Schema",
		"",
		"After the section: `gleann memory import-graph` again.",
		"",
	}, "\n")

	body, ok := sectionByTitle(doc, memoryUpgradeTitle)
	if !ok {
		t.Fatalf("sectionByTitle did not find %q", memoryUpgradeTitle)
	}
	if !strings.Contains(body, "Run `gleann memory import-graph` here.") {
		t.Errorf("section body misses the direct section text: %q", body)
	}
	if !strings.Contains(body, "Still part of the upgrade section.") {
		t.Errorf("section body drops a deeper subsection, which is still part of the section: %q", body)
	}
	if strings.Contains(body, "After the section") {
		t.Errorf("section body continues past the next same-level heading: %q", body)
	}
	if strings.Contains(body, "appears before the upgrade section") {
		t.Errorf("section body includes text from before the heading: %q", body)
	}

	// The production check must not accept the command when it only appears
	// outside the section.
	without := strings.Replace(doc, "Run `gleann memory import-graph` here.", "Nothing here.", 1)
	if got, ok := sectionByTitle(without, memoryUpgradeTitle); !ok || strings.Contains(got, "import-graph") {
		t.Errorf("a command outside the upgrade section must not count; got ok=%v body=%q", ok, got)
	}
}

// TestActiveDocFilesIncludesReadme guards MINOR 5: the README is an active
// document too, so a bbolt/Kùzu/daemon claim there must fail this suite.
func TestActiveDocFilesIncludesReadme(t *testing.T) {
	root := repoRoot(t)
	want := filepath.Join(root, "README.md")
	for _, path := range activeDocFiles(t, root) {
		if path == want {
			return
		}
	}
	t.Errorf("activeDocFiles does not scan %s: README.md is a current document and must be covered", want)
}

// TestLegacyScannerIgnoresKuzuBenchmarkAndUpgradeContext keeps the new Kùzu
// token from turning the legitimate historical/benchmark lines in the shipped
// docs into failures.
func TestLegacyScannerIgnoresKuzuBenchmarkAndUpgradeContext(t *testing.T) {
	doc := strings.Join([]string{
		"# History",
		"",
		"## Graph database benchmark (`bench/graphdb`)",
		"",
		"`go.mod`. Kùzu, removed from gleann, is the first candidate.",
		"",
		"```bash",
		"make bench-graphdb GRAPHDB_TAGS=kuzu GRAPHDB_EDGES=100000,1000000",
		"```",
		"",
		"[decisions/kuzu-to-sqlite.md](decisions/kuzu-to-sqlite.md) for the comparison",
		"that led to the removal of Kùzu.",
		"",
		"## Storage",
		"",
		"Older releases kept each store in a KuzuDB database.",
		"",
		"An index built by gleann v1.9 in a Kùzu store is converted by `gleann index migrate`.",
		"",
	}, "\n")

	if hits := legacyHitsInText("history.md", doc); len(hits) > 0 {
		t.Errorf("legitimate historical/benchmark Kùzu lines were reported as current architecture:\n  %s", strings.Join(hits, "\n  "))
	}
}

// TestLegacyScannerNestedHeadingKeepsUpgradeSection guards MAJOR 1: a deeper
// subheading inside "## Upgrading entities from v1.9" must not close the
// section and re-expose its bbolt/Kùzu instructions as current architecture.
func TestLegacyScannerNestedHeadingKeepsUpgradeSection(t *testing.T) {
	doc := strings.Join([]string{
		"# Memory",
		"",
		memoryUpgradeHeading,
		"",
		"### Verification Steps",
		"",
		"The exporter opens bbolt and reads internal/graph/kuzu directly.",
		"",
		"### More Detail",
		"",
		"Still inside the upgrade instructions.",
		"",
	}, "\n")
	if hits := legacyHitsInText("upgrade.md", doc); len(hits) > 0 {
		t.Errorf("a deeper subheading closed the legacy section:\n  %s", strings.Join(hits, "\n  "))
	}

	// The section must still end at the next same-level heading.
	exiting := doc + "\n## Current Schema\n\nThe current schema only.\n"
	if hits := legacyHitsInText("upgrade.md", exiting); len(hits) > 0 {
		t.Errorf("text after the section was still exempt:\n  %s", strings.Join(hits, "\n  "))
	}

	// ... and a violation after it must be caught.
	violating := doc + "\n## Current Schema\n\nEvery process opens bbolt directly.\n"
	hits := legacyHitsInText("upgrade.md", violating)
	if len(hits) != 1 || !strings.Contains(hits[0], "BBolt") {
		t.Errorf("expected exactly the post-section bbolt line to be reported, got %v", hits)
	}
}

// TestLegacyScannerScansActiveMigrationSections guards MAJOR 2: broad words
// such as "Migration" and "Comparison" must not exempt a current section.
func TestLegacyScannerScansActiveMigrationSections(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{
			name: "models migration section",
			doc:  "# Models\n\n## 3. Embedding Model Consistency & Migration\n\nWe still use bbolt for memory.\n",
		},
		{
			name: "models migrating subsection",
			doc:  "# Models\n\n### Migrating an Index to a New Model\n\nbbolt stores the memory blocks.\n",
		},
		{
			name: "architecture comparison section",
			doc:  "# Architecture\n\n### Architecture Comparison\n\nThe memory daemon is managed by EnsureDaemon.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := legacyHitsInText("active.md", tc.doc)
			if len(hits) != 1 {
				t.Errorf("a current section was exempted (want 1 hit, got %d): %v", len(hits), hits)
			}
		})
	}
}

// TestLegacyScannerChecksOrdinaryProse guards MAJOR 3: an ordinary sentence
// that happens to contain "upgraded" or "comparison" is not history.
func TestLegacyScannerChecksOrdinaryProse(t *testing.T) {
	cases := []string{
		"## Memory Store\n\nWe upgraded the memory store: every process opens bbolt directly.\n",
		"## Daemon\n\nIn comparison to other tools, the memory daemon is managed by EnsureDaemon.\n",
	}
	for _, doc := range cases {
		if hits := legacyHitsInText("prose.md", doc); len(hits) != 1 {
			t.Errorf("prose framing was treated as an exemption (want 1 hit, got %d): %v", len(hits), hits)
		}
	}
}

// TestLegacyScannerDetectsKuzuBackend guards MINOR 4: naming Kùzu (or KuzuDB)
// as the graph store is a violation even without the old package path.
func TestLegacyScannerDetectsKuzuBackend(t *testing.T) {
	cases := []string{
		"## Graph Storage\n\nThe analysis server loads every index into Kùzu.\n",
		"## Graph Storage\n\nThe server keeps the graph in a KuzuDB database.\n",
		"## Graph Storage\n\nEach index has its own kuzu store.\n",
	}
	for _, doc := range cases {
		hits := legacyHitsInText("kuzu.md", doc)
		if len(hits) != 1 || !strings.Contains(hits[0], "Kùzu graph backend") {
			t.Errorf("Kùzu as the current backend was not detected: %v", hits)
		}
	}
}

// TestLegacyScannerExemptsOnlyExplicitHistory pins the narrow line rule used
// to keep the shipped docs green: an explicit removal statement is exempt, a
// neutral current-tense mention is not.
func TestLegacyScannerExemptsOnlyExplicitHistory(t *testing.T) {
	exempt := "## Graph Storage\n\nKùzu was removed in v1.20 and replaced by the SQLite graph store.\n"
	if hits := legacyHitsInText("history.md", exempt); len(hits) != 0 {
		t.Errorf("an explicit removal statement was reported as current architecture: %v", hits)
	}
	current := "## Graph Storage\n\nThe graph is stored in Kùzu.\n"
	if hits := legacyHitsInText("current.md", current); len(hits) != 1 {
		t.Errorf("a current-tense Kùzu mention was not reported: %v", hits)
	}
}
