package memory

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ExportStats summarizes the outcome of an export operation.
type ExportStats struct {
	Blocks    int
	Summaries int
	Pruned    int // stale files removed from the export directory
}

// ExportOptions selects what an export run writes.
type ExportOptions struct {
	// Scope restricts the export to that scope plus global blocks and its
	// path ancestors. Empty (or the "all" sentinel) exports every scope.
	Scope string
	// IncludeShort also writes the session-scoped short-term blocks. They are
	// skipped by default because they are about to disappear anyway.
	IncludeShort bool
	// IncludeExpired also writes blocks whose ExpiresAt is already past.
	IncludeExpired bool
}

// ImportStats summarizes the outcome of an import operation.
type ImportStats struct {
	BlocksTotal       int      // Total block files read
	BlocksImported    int      // New blocks inserted
	BlocksUpdated     int      // Existing blocks updated (newer updated_at in file)
	BlocksUnchanged   int      // Existing blocks preserved unchanged
	Conflicts         int      // Existing blocks encountered with conflicting fields
	HandEdited        int      // Files edited by hand while updated_at stayed behind
	HandEditedFiles   []string // Their file names, for the operator to review
	SummariesTotal    int      // Total summary files read
	SummariesImported int      // Summaries saved/updated
	DryRun            bool     // Set when the run only previewed the outcome
}

// ImportOptions controls an import run.
type ImportOptions struct {
	// DryRun parses and compares every file but writes nothing: no SQLite row
	// changes and no short-term block is added.
	DryRun bool

	// CurrentScope is the collision-safe scope of the directory the import
	// runs from; empty means DetectRepoScope(). A bare (pre-v1.22) scope name
	// carried by the imported blocks is registered as a legacy alias when it
	// equals this directory's folder name, so an old backup restored into a
	// fresh store stays visible from that directory. Names that do not match
	// are imported as plain explicit scopes and are not aliased.
	CurrentScope string
}

// blockFrontMatter represents the YAML front matter of a memory block file.
type blockFrontMatter struct {
	ID             string            `yaml:"id"`
	Tier           Tier              `yaml:"tier"`
	Label          string            `yaml:"label"`
	Source         string            `yaml:"source,omitempty"`
	Tags           []string          `yaml:"tags,omitempty"`
	Metadata       map[string]string `yaml:"metadata,omitempty"`
	CreatedAt      time.Time         `yaml:"created_at"`
	UpdatedAt      time.Time         `yaml:"updated_at"`
	ExpiresAt      *time.Time        `yaml:"expires_at,omitempty"`
	CharLimit      int               `yaml:"char_limit,omitempty"`
	Scope          string            `yaml:"scope,omitempty"`
	Repo           string            `yaml:"repo,omitempty"`
	Paths          []string          `yaml:"paths,omitempty"`
	Symbols        []string          `yaml:"symbols,omitempty"`
	CommitHash     string            `yaml:"commit_hash,omitempty"`
	Commit         string            `yaml:"commit,omitempty"` // Alias for compatibility
	Suspect        bool              `yaml:"suspect,omitempty"`
	StaleReason    string            `yaml:"stale_reason,omitempty"`
	Confirms       int               `yaml:"confirms,omitempty"`
	Conflicts      int               `yaml:"conflicts,omitempty"`
	AccessCount    int               `yaml:"access_count,omitempty"`
	LastAccessedAt *time.Time        `yaml:"last_accessed_at,omitempty"`
}

// summaryFrontMatter represents the YAML front matter of a conversation summary file.
type summaryFrontMatter struct {
	ConversationID string    `yaml:"conversation_id"`
	Title          string    `yaml:"title"`
	MessageCount   int       `yaml:"message_count,omitempty"`
	IndexNames     []string  `yaml:"index_names,omitempty"`
	Model          string    `yaml:"model,omitempty"`
	CreatedAt      time.Time `yaml:"created_at"`
}

var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// sanitizeFilename sanitizes s for safe use as a file name across platforms (Windows, Linux, macOS).
func sanitizeFilename(s string) string {
	if s == "" {
		return "_unnamed"
	}
	var sb strings.Builder
	for _, r := range s {
		// Only [A-Za-z0-9._-] survive. That excludes every Windows-reserved
		// character, including ':' (an NTFS alternate data stream marker);
		// the hash suffix of the generated name keeps sanitized IDs apart.
		if isSafeNameRune(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	cleaned := strings.TrimRight(sb.String(), ". ")
	if cleaned == "" {
		cleaned = "_unnamed"
	}
	if windowsReservedNames[strings.ToUpper(cleaned)] {
		cleaned = "_" + cleaned
	}
	return cleaned
}

// isSafeNameRune reports whether r is in the generated-name character set
// [A-Za-z0-9._-].
func isSafeNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		r == '.' || r == '_' || r == '-'
}

// maxNameRunes bounds the readable part of a file name; the hash suffix keeps
// it unique, and long IDs stay under path-length limits.
const maxNameRunes = 64

// keyHash returns 8 hex characters of SHA-256 over the exact key.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// truncateRunes keeps at most n runes of s.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), ". ")
}

// safeBlockFilename returns a deterministic, Windows-safe filename for a
// memory block ID: a readable sanitized prefix plus a hash of the exact ID.
// The hash keeps two IDs apart that sanitize alike ("a:b" and "a_b") or differ
// only in case ("Ab" and "ab" on Windows and macOS), so no file overwrites
// another. Import reads the ID from the front matter, not the name.
func safeBlockFilename(id string) string {
	return truncateRunes(sanitizeFilename(id), maxNameRunes) + "-" + keyHash(id) + ".md"
}

// safeSummaryFilename returns a deterministic, Windows-safe filename for a
// conversation summary. The hash covers the store key (creation time to the
// nanosecond plus conversation ID), so two summaries of one conversation in
// the same second get different files.
func safeSummaryFilename(createdAt time.Time, convID string) string {
	prefix := createdAt.Format("20060102_150405")
	base := truncateRunes(sanitizeFilename(convID), maxNameRunes)
	return fmt.Sprintf("%s_%s-%s.md", prefix, base, keyHash(createdAt.UTC().Format(time.RFC3339Nano)+"_"+convID))
}

// splitFrontMatter separates YAML front matter from markdown body.
// The file must start with "---" on its first line, and front matter ends at the first matching "---" line thereafter.
func splitFrontMatter(data []byte) ([]byte, []byte, error) {
	var pos int
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		pos = 5
	} else if bytes.HasPrefix(data, []byte("---\n")) {
		pos = 4
	} else {
		return nil, nil, fmt.Errorf("file does not start with '---' front matter delimiter")
	}

	closingStart := -1
	bodyStart := -1

	cur := pos
	for cur < len(data) {
		lineStart := cur
		lineEnd := bytes.IndexByte(data[cur:], '\n')
		var nextCur int
		if lineEnd == -1 {
			lineEnd = len(data)
			nextCur = len(data)
		} else {
			lineEnd = cur + lineEnd
			nextCur = lineEnd + 1
		}

		line := bytes.TrimSpace(data[lineStart:lineEnd])
		if bytes.Equal(line, []byte("---")) {
			closingStart = lineStart
			bodyStart = nextCur
			break
		}
		cur = nextCur
	}

	if closingStart == -1 {
		return nil, nil, fmt.Errorf("closing '---' front matter delimiter not found")
	}

	yamlPart := data[pos:closingStart]
	bodyPart := data[bodyStart:]
	return yamlPart, bodyPart, nil
}

// BlockToMarkdown serializes a memory block to markdown bytes with YAML front matter.
func BlockToMarkdown(b *Block) ([]byte, error) {
	fm := blockFrontMatter{
		ID:             b.ID,
		Tier:           b.Tier,
		Label:          b.Label,
		Source:         b.Source,
		Tags:           b.Tags,
		Metadata:       b.Metadata,
		CreatedAt:      b.CreatedAt.UTC(),
		UpdatedAt:      b.UpdatedAt.UTC(),
		ExpiresAt:      b.ExpiresAt,
		CharLimit:      b.CharLimit,
		Scope:          b.Scope,
		Repo:           b.Repo,
		Paths:          b.Paths,
		Symbols:        b.Symbols,
		CommitHash:     b.Commit,
		Suspect:        b.Suspect,
		StaleReason:    b.StaleReason,
		Confirms:       b.Confirms,
		Conflicts:      b.Conflicts,
		AccessCount:    b.AccessCount,
		LastAccessedAt: b.LastAccessedAt,
	}

	yamlData, err := yaml.Marshal(&fm)
	if err != nil {
		return nil, fmt.Errorf("marshaling block %s front matter: %w", b.ID, err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlData)
	buf.WriteString("---\n")
	buf.WriteString(b.Content)
	return buf.Bytes(), nil
}

// MarkdownToBlock deserializes markdown bytes with YAML front matter into a Block.
func MarkdownToBlock(data []byte) (*Block, error) {
	yamlPart, bodyPart, err := splitFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("parsing front matter: %w", err)
	}

	var fm blockFrontMatter
	if err := yaml.Unmarshal(yamlPart, &fm); err != nil {
		return nil, fmt.Errorf("decoding block front matter: %w", err)
	}

	commit := fm.CommitHash
	if commit == "" {
		commit = fm.Commit
	}

	b := &Block{
		ID:             fm.ID,
		Tier:           fm.Tier,
		Label:          fm.Label,
		Content:        string(bodyPart),
		Source:         fm.Source,
		Tags:           fm.Tags,
		Metadata:       fm.Metadata,
		CreatedAt:      fm.CreatedAt,
		UpdatedAt:      fm.UpdatedAt,
		ExpiresAt:      fm.ExpiresAt,
		CharLimit:      fm.CharLimit,
		Scope:          fm.Scope,
		Repo:           fm.Repo,
		Paths:          fm.Paths,
		Symbols:        fm.Symbols,
		Commit:         commit,
		Suspect:        fm.Suspect,
		StaleReason:    fm.StaleReason,
		Confirms:       fm.Confirms,
		Conflicts:      fm.Conflicts,
		AccessCount:    fm.AccessCount,
		LastAccessedAt: fm.LastAccessedAt,
	}
	return b, nil
}

// SummaryToMarkdown serializes a conversation summary to markdown bytes with YAML front matter.
func SummaryToMarkdown(s *Summary) ([]byte, error) {
	fm := summaryFrontMatter{
		ConversationID: s.ConversationID,
		Title:          s.Title,
		MessageCount:   s.MessageCount,
		IndexNames:     s.IndexNames,
		Model:          s.Model,
		CreatedAt:      s.CreatedAt.UTC(),
	}

	yamlData, err := yaml.Marshal(&fm)
	if err != nil {
		return nil, fmt.Errorf("marshaling summary front matter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlData)
	buf.WriteString("---\n")
	buf.WriteString(s.Content)
	return buf.Bytes(), nil
}

// MarkdownToSummary deserializes markdown bytes with YAML front matter into a Summary.
func MarkdownToSummary(data []byte) (*Summary, error) {
	yamlPart, bodyPart, err := splitFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("parsing front matter: %w", err)
	}

	var fm summaryFrontMatter
	if err := yaml.Unmarshal(yamlPart, &fm); err != nil {
		return nil, fmt.Errorf("decoding summary front matter: %w", err)
	}

	s := &Summary{
		ConversationID: fm.ConversationID,
		Title:          fm.Title,
		Content:        string(bodyPart),
		MessageCount:   fm.MessageCount,
		IndexNames:     fm.IndexNames,
		Model:          fm.Model,
		CreatedAt:      fm.CreatedAt,
	}
	return s, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func timePtrEqual(t1, t2 *time.Time) bool {
	if t1 == nil && t2 == nil {
		return true
	}
	if t1 == nil || t2 == nil {
		return false
	}
	return t1.Equal(*t2)
}

func blocksEqual(a, b *Block) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID &&
		a.Tier == b.Tier &&
		a.Label == b.Label &&
		a.Content == b.Content &&
		a.Source == b.Source &&
		slicesEqual(a.Tags, b.Tags) &&
		mapsEqual(a.Metadata, b.Metadata) &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt) &&
		timePtrEqual(a.ExpiresAt, b.ExpiresAt) &&
		a.CharLimit == b.CharLimit &&
		a.Scope == b.Scope &&
		a.Repo == b.Repo &&
		slicesEqual(a.Paths, b.Paths) &&
		slicesEqual(a.Symbols, b.Symbols) &&
		a.Commit == b.Commit &&
		a.Suspect == b.Suspect &&
		a.StaleReason == b.StaleReason &&
		a.Confirms == b.Confirms &&
		a.Conflicts == b.Conflicts &&
		a.AccessCount == b.AccessCount &&
		timePtrEqual(a.LastAccessedAt, b.LastAccessedAt)
}

// Export writes all memory blocks (one markdown file each) and summaries (in a "summaries" subdirectory)
// to the specified directory. Every tier, including the short-term blocks and
// expired ones, is written: the plain Export is the lossless full dump.
func (s *Store) Export(dir string) (*ExportStats, error) {
	return s.ExportWithOptions(dir, ExportOptions{IncludeShort: true, IncludeExpired: true})
}

// ExportWithOptions writes blocks and summaries to dir according to opts. The
// CLI default skips short-term and expired blocks and exports every scope;
// opts.Scope narrows the run to one scope (plus global and its ancestors).
//
// Each export run records the files it wrote in a hidden manifest inside the
// export directory. A later run for the same scope deletes the files listed
// there that it no longer writes — blocks forgotten in the meantime — and
// touches nothing else in the directory, so a shared backup folder keeps the
// user's own files.
func (s *Store) ExportWithOptions(dir string, opts ExportOptions) (*ExportStats, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating export directory %s: %w", dir, err)
	}

	scope := NormalizeScope(opts.Scope)
	if IsAllScopesSentinel(scope) {
		scope = ""
	}
	var allowed map[string]struct{}
	if scope != "" {
		allowed = scopeAncestors(scope)
		// Pre-v1.22 bare-name blocks stay readable from their prefixed scope.
		for _, a := range s.scopeAliases(scope) {
			allowed[a] = struct{}{}
		}
	}
	keepBlock := func(b *Block) bool {
		if !opts.IncludeShort && b.Tier == TierShort {
			return false
		}
		if !opts.IncludeExpired && b.IsExpired() {
			return false
		}
		if allowed != nil {
			if _, ok := allowed[b.Scope]; !ok {
				return false
			}
		}
		return true
	}

	stats := &ExportStats{}
	written := []string{}

	// 1. Gather all blocks (short-term from memory, medium/long from SQLite).
	var allBlocks []Block
	seenIDs := make(map[string]bool)

	for _, b := range s.shortTerm {
		allBlocks = append(allBlocks, b)
		seenIDs[b.ID] = true
	}

	rows, err := s.reader.Query(`SELECT ` + blockCols + ` FROM blocks ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("listing blocks for export: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, fmt.Errorf("reading block for export: %w", err)
		}
		if !seenIDs[b.ID] {
			allBlocks = append(allBlocks, b)
			seenIDs[b.ID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing blocks for export: %w", err)
	}
	rows.Close()

	sort.SliceStable(allBlocks, func(i, j int) bool {
		if allBlocks[i].CreatedAt.Equal(allBlocks[j].CreatedAt) {
			return allBlocks[i].ID < allBlocks[j].ID
		}
		return allBlocks[i].CreatedAt.After(allBlocks[j].CreatedAt)
	})
	for i := range allBlocks {
		b := &allBlocks[i]
		if !keepBlock(b) {
			continue
		}
		md, err := BlockToMarkdown(b)
		if err != nil {
			return nil, err
		}

		fname := safeBlockFilename(b.ID) // unique per ID, see safeBlockFilename

		outPath := filepath.Join(dir, fname)
		if err := os.WriteFile(outPath, md, 0o644); err != nil {
			return nil, fmt.Errorf("writing block file %s: %w", outPath, err)
		}
		written = append(written, fname)
		stats.Blocks++
	}

	// 2. Export summaries.
	summaries, err := s.ListSummaries()
	if err != nil {
		return nil, fmt.Errorf("listing summaries for export: %w", err)
	}

	if len(summaries) > 0 {
		sumDir := filepath.Join(dir, "summaries")
		if err := os.MkdirAll(sumDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating summaries directory %s: %w", sumDir, err)
		}
		for i := range summaries {
			sum := &summaries[i]
			md, err := SummaryToMarkdown(sum)
			if err != nil {
				return nil, err
			}

			fname := safeSummaryFilename(sum.CreatedAt, sum.ConversationID)

			outPath := filepath.Join(sumDir, fname)
			if err := os.WriteFile(outPath, md, 0o644); err != nil {
				return nil, fmt.Errorf("writing summary file %s: %w", outPath, err)
			}
			written = append(written, path.Join("summaries", fname))
			stats.Summaries++
		}
	}

	// 3. Prune files this scope's previous run wrote but this one does not,
	// then record what this run wrote.
	pruned, err := s.applyExportManifest(dir, scope, written)
	if err != nil {
		return nil, err
	}
	stats.Pruned = pruned

	return stats, nil
}

// exportManifestName is the bookkeeping file Export writes inside the export
// directory. It is a ".json" file, so Import (which reads "*.md") ignores it.
const exportManifestName = ".gleann-export-manifest.json"

// exportManifestVersion is the schema version of the manifest file.
const exportManifestVersion = 1

// exportManifest records, per export scope, the file names (relative to the
// export directory) each run wrote. The scope key is the normalized scope, or
// "all" for an unscoped export, so a scoped run never prunes another scope's
// files.
type exportManifest struct {
	Version int                 `json:"version"`
	Files   map[string][]string `json:"files"`
}

// exportScopeKey names the manifest bucket an export run belongs to.
func exportScopeKey(scope string) string {
	if scope == "" {
		return "all"
	}
	return scope
}

// applyExportManifest deletes the files the previous run of the same scope
// recorded but this run did not write, then stores the new list. Files that are
// not in the manifest are never removed.
func (s *Store) applyExportManifest(dir, scope string, written []string) (int, error) {
	manifest := readExportManifest(dir)
	key := exportScopeKey(scope)

	keep := make(map[string]bool, len(written))
	for _, name := range written {
		keep[name] = true
	}

	pruned := 0
	var skippedPath string
	skipped := 0
	for _, name := range manifest.Files[key] {
		// Manifests are stored with forward slashes; tolerate backslash entries
		// an older Windows run may have written.
		name = filepath.ToSlash(name)
		if keep[name] || name == exportManifestName || !safeExportRelPath(name) || !isExportedFileName(name) {
			continue
		}
		removed, linkAt, err := removeExportedFileReason(dir, name)
		if linkAt != "" {
			if skipped == 0 {
				skippedPath = linkAt
			}
			skipped++
		}
		if err != nil {
			return pruned, fmt.Errorf("removing stale export file %s: %w", filepath.Join(dir, name), err)
		}
		if removed {
			pruned++
		}
	}

	if skipped > 0 {
		// One warning per run, however many files were skipped.
		warnf("warning: memory export: %d stale file(s) not pruned because %s is a symlink, junction or other reparse point (for example a OneDrive folder); gleann never deletes through those. Remove the stale files by hand if wanted", skipped, skippedPath)
	}

	if manifest.Files == nil {
		manifest.Files = map[string][]string{}
	}
	// The manifest always records forward-slash relative paths, on every OS.
	sorted := make([]string, len(written))
	for i, w := range written {
		sorted[i] = filepath.ToSlash(w)
	}
	sort.Strings(sorted)
	manifest.Files[key] = sorted
	manifest.Version = exportManifestVersion

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return pruned, fmt.Errorf("encoding export manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName), append(data, '\n'), 0o644); err != nil {
		return pruned, fmt.Errorf("writing export manifest: %w", err)
	}
	return pruned, nil
}

// exportedFileRe matches the names Export generates: "<name>-<8hex>.md" in the
// export directory or in its "summaries" subdirectory. A manifest entry with
// any other shape is never deleted, however it got into the manifest.
var exportedFileRe = regexp.MustCompile(`^(summaries/)?[A-Za-z0-9._-]+-[0-9a-f]{8}\.md$`)

func isExportedFileName(name string) bool {
	return exportedFileRe.MatchString(filepath.ToSlash(name))
}

// removeExportedFile deletes the regular file name under dir without ever
// following a symlink: the export root is resolved once, then every path
// component below it is Lstat-ed and a symlinked directory or file refuses the
// removal. It reports whether a file was deleted; a missing file is not an
// error.
func removeExportedFile(dir, name string) (bool, error) {
	removed, _, err := removeExportedFileReason(dir, name)
	return removed, err
}

// warnf reports a non-fatal export problem; a seam so tests can capture it.
var warnf = log.Printf

// removeExportedFileReason is removeExportedFile that also returns the path
// of the symlink, junction or reparse point that made it refuse ("" when the
// refusal had another reason or nothing was refused).
func removeExportedFileReason(dir, name string) (bool, string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false, "", err
	}
	cur := root
	parts := strings.Split(filepath.ToSlash(filepath.Clean(name)), "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return false, "", nil
			}
			return false, "", err
		}
		// Symlinks, Windows junctions and other reparse points are refused.
		if fileIsLinkLike(fi) {
			return false, cur, nil
		}
		last := i == len(parts)-1
		if last && !fi.Mode().IsRegular() {
			return false, "", nil
		}
		if !last && !fi.IsDir() {
			return false, "", nil
		}
	}
	if rel, err := filepath.Rel(root, cur); err != nil || strings.HasPrefix(rel, "..") {
		return false, "", nil
	}
	if err := os.Remove(cur); err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, "", nil
}

// readExportManifest loads the manifest of an export directory. A missing or
// unreadable manifest yields an empty one: pruning then removes nothing, which
// is the safe reading of an export directory gleann did not write.
func readExportManifest(dir string) exportManifest {
	out := exportManifest{Version: exportManifestVersion}
	data, err := os.ReadFile(filepath.Join(dir, exportManifestName))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Files == nil {
		return exportManifest{Version: exportManifestVersion}
	}
	return out
}

// safeExportRelPath rejects manifest entries that would escape the export
// directory (absolute paths, "..", empty names).
func safeExportRelPath(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	clean := filepath.Clean(name)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// Import reads memory block markdown files and summaries from dir.
// Import is lossless and idempotent: re-importing unchanged files changes nothing.
// If an existing block with the same ID conflicts, the version with the newer updated_at is kept,
// and the conflict is counted.
func (s *Store) Import(dir string) (*ImportStats, error) {
	return s.ImportWithOptions(dir, ImportOptions{})
}

// ImportWithOptions imports the markdown files of dir; with
// ImportOptions.DryRun nothing is written and the stats describe what an import
// would do. Either way the run reports hand-edited files: a file whose content
// differs from the stored block while its updated_at did not move forward is
// not silently skipped, it is listed in ImportStats.HandEditedFiles.
func (s *Store) ImportWithOptions(dir string, opts ImportOptions) (*ImportStats, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("reading import directory %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("import path is not a directory: %s", dir)
	}

	stats := &ImportStats{DryRun: opts.DryRun}
	current := opts.CurrentScope
	if current == "" && !opts.DryRun {
		current = DetectRepoScope()
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading import directory %s: %w", dir, err)
	}

	var blockFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			blockFiles = append(blockFiles, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(blockFiles)

	type incoming = incomingBlock
	var blocks []incomingBlock
	for _, fpath := range blockFiles {
		data, err := os.ReadFile(fpath)
		if err != nil {
			return nil, fmt.Errorf("reading block file %s: %w", fpath, err)
		}
		b, err := MarkdownToBlock(data)
		if err != nil {
			return nil, fmt.Errorf("parsing block file %s: %w", fpath, err)
		}
		// Import is a scope write path: canonicalize front-matter scopes.
		normalizeBlockScopes(b)
		blocks = append(blocks, incoming{file: fpath, block: b})
		stats.BlocksTotal++
	}

	// Read summaries if summaries/ directory exists
	sumDir := filepath.Join(dir, "summaries")
	var incomingSummaries []*Summary
	if sfi, err := os.Stat(sumDir); err == nil && sfi.IsDir() {
		sumEntries, err := os.ReadDir(sumDir)
		if err != nil {
			return nil, fmt.Errorf("reading summaries directory %s: %w", sumDir, err)
		}
		var sumFiles []string
		for _, e := range sumEntries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				sumFiles = append(sumFiles, filepath.Join(sumDir, e.Name()))
			}
		}
		sort.Strings(sumFiles)

		for _, fpath := range sumFiles {
			data, err := os.ReadFile(fpath)
			if err != nil {
				return nil, fmt.Errorf("reading summary file %s: %w", fpath, err)
			}
			sum, err := MarkdownToSummary(data)
			if err != nil {
				return nil, fmt.Errorf("parsing summary file %s: %w", fpath, err)
			}
			incomingSummaries = append(incomingSummaries, sum)
			stats.SummariesTotal++
		}
	}

	if opts.DryRun {
		if err := s.planImport(blocks, incomingSummaries, stats); err != nil {
			return nil, err
		}
		return stats, nil
	}

	// 1. Apply short-term blocks in-memory
	for _, in := range blocks {
		b := in.block
		if b.Tier != TierShort {
			continue
		}
		var existing *Block
		for i := range s.shortTerm {
			if s.shortTerm[i].ID == b.ID {
				existing = &s.shortTerm[i]
				break
			}
		}
		switch classifyImport(existing, b, in.file, stats) {
		case decisionAdd:
			s.shortTerm = append(s.shortTerm, *b)
			stats.BlocksImported++
		case decisionUpdate:
			*existing = *b
			stats.BlocksUpdated++
		default:
			stats.BlocksUnchanged++
		}
	}

	// 2. Apply persisted blocks and summaries to SQLite inside writeTx
	var aliased map[string]bool
	err = s.writeTx(func(tx *sql.Tx) error {
		for _, in := range blocks {
			b := in.block
			if b.Tier == TierShort {
				continue
			}
			existing, seq, err := getBlockTx(tx, b.ID)
			if err != nil {
				return fmt.Errorf("reading existing block %s: %w", b.ID, err)
			}
			switch classifyImport(existing, b, in.file, stats) {
			case decisionAdd:
				seq, err = insertBlockTx(tx, b)
				if err != nil {
					return err
				}
				if err := insertLexTx(tx, seq, s.lexTokens(b)); err != nil {
					return err
				}
				stats.BlocksImported++
			case decisionUpdate:
				if err := updateBlockTx(tx, b); err != nil {
					return err
				}
				if err := refreshLexTx(tx, seq, s.lexTokens(b)); err != nil {
					return err
				}
				stats.BlocksUpdated++
			default:
				stats.BlocksUnchanged++
			}
		}

		for _, sum := range incomingSummaries {
			data, err := json.Marshal(sum)
			if err != nil {
				return fmt.Errorf("encoding summary %s: %w", sum.ConversationID, err)
			}
			key := fmt.Sprintf("%s_%s", sum.CreatedAt.Format("20060102_150405"), sum.ConversationID)
			if _, err := tx.Exec(`INSERT INTO summaries (key, data) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET data = excluded.data`, key, string(data)); err != nil {
				return fmt.Errorf("saving summary %s: %w", key, err)
			}
			stats.SummariesImported++
		}

		var err error
		aliased, err = registerImportedLegacyScopesTx(tx, blocks, current, s.legacyScopes)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("importing into memory database: %w", err)
	}
	if aliased != nil {
		s.legacyScopes = aliased
	}

	return stats, nil
}

// incomingBlock is one parsed markdown block file together with the file it
// came from, so import errors and hand-edit reports can name the file.
type incomingBlock struct {
	file  string
	block *Block
}

// importDecision is what should happen to one incoming block.
type importDecision int

const (
	decisionAdd importDecision = iota
	decisionUpdate
	decisionUnchanged
)

// classifyImport compares an incoming block with the stored one, accounts for
// the counters that do not depend on writing (Conflicts, HandEdited) and says
// whether the caller must insert, update or leave the block alone.
//
// A file that differs from the stored block while its updated_at did not move
// forward is a hand-edited export: importing it would throw away the user's
// edit, so it is reported instead of being counted as a plain no-op.
func classifyImport(existing, incoming *Block, file string, stats *ImportStats) importDecision {
	if existing == nil {
		return decisionAdd
	}
	if blocksEqual(existing, incoming) {
		return decisionUnchanged
	}
	stats.Conflicts++
	if incoming.UpdatedAt.After(existing.UpdatedAt) {
		return decisionUpdate
	}
	if incoming.Content != existing.Content {
		stats.HandEdited++
		stats.HandEditedFiles = append(stats.HandEditedFiles, filepath.Base(file))
	}
	return decisionUnchanged
}

// planImport runs the same comparison as an import against a read-only view of
// the store, so --dry-run reports exactly what a real run would change without
// writing anything.
func (s *Store) planImport(blocks []incomingBlock, summaries []*Summary, stats *ImportStats) error {
	for _, in := range blocks {
		b := in.block
		var existing *Block
		if b.Tier == TierShort {
			for i := range s.shortTerm {
				if s.shortTerm[i].ID == b.ID {
					existing = &s.shortTerm[i]
					break
				}
			}
		} else {
			stored, _, err := getBlockTx(s.reader, b.ID)
			if err != nil {
				return fmt.Errorf("reading existing block %s: %w", b.ID, err)
			}
			existing = stored
		}
		switch classifyImport(existing, b, in.file, stats) {
		case decisionAdd:
			stats.BlocksImported++
		case decisionUpdate:
			stats.BlocksUpdated++
		default:
			stats.BlocksUnchanged++
		}
	}
	// Summaries are upserted unconditionally, so every summary file counts as
	// imported in a preview as well as in a real run.
	stats.SummariesImported += len(summaries)
	return nil
}

// registerImportedLegacyScopesTx records, in the legacy-alias ledger, the bare
// scope name of the current directory when the imported blocks carry it. It
// returns the new alias set, or nil when nothing changed. The change commits
// with the import, so a failed import leaves the ledger untouched.
func registerImportedLegacyScopesTx(tx *sql.Tx, blocks []incomingBlock, current string, have map[string]bool) (map[string]bool, error) {
	base, ok := legacyAliasScope(current)
	if !ok {
		return nil, nil
	}
	if _, known := matchLegacyName(runtime.GOOS, have, base); known {
		return nil, nil
	}
	found := false
	for _, in := range blocks {
		if in.block.Scope == base {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	merged := map[string]bool{base: true}
	for n := range have {
		merged[n] = true
	}
	names := make([]string, 0, len(merged))
	for n := range merged {
		names = append(names, n)
	}
	sort.Strings(names)
	data, err := json.Marshal(names)
	if err != nil {
		return nil, fmt.Errorf("encoding legacy scope names: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, scopeLegacyNamesKey, string(data)); err != nil {
		return nil, fmt.Errorf("recording imported legacy scope name: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, '1')
		ON CONFLICT(key) DO UPDATE SET value = '1'`, scopeCollisionMigration); err != nil {
		return nil, fmt.Errorf("recording scope collision migration: %w", err)
	}
	return merged, nil
}
