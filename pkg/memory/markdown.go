package memory

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ExportStats summarizes the outcome of an export operation.
type ExportStats struct {
	Blocks    int
	Summaries int
}

// ImportStats summarizes the outcome of an import operation.
type ImportStats struct {
	BlocksTotal       int // Total block files read
	BlocksImported    int // New blocks inserted
	BlocksUpdated     int // Existing blocks updated (newer updated_at in file)
	BlocksUnchanged   int // Existing blocks preserved unchanged
	Conflicts         int // Existing blocks encountered with conflicting fields
	SummariesTotal    int // Total summary files read
	SummariesImported int // Summaries saved/updated
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
		// Reserved characters on Windows: < > : " / \ | ? * and ASCII control characters 0-31
		if r < 32 || r == '<' || r == '>' || r == ':' || r == '"' ||
			r == '/' || r == '\\' || r == '|' || r == '?' || r == '*' {
			sb.WriteRune('_')
		} else {
			sb.WriteRune(r)
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
// to the specified directory.
func (s *Store) Export(dir string) (*ExportStats, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating export directory %s: %w", dir, err)
	}

	stats := &ExportStats{}

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

	sort.SliceStable(allBlocks, func(i, j int) bool {
		if allBlocks[i].CreatedAt.Equal(allBlocks[j].CreatedAt) {
			return allBlocks[i].ID < allBlocks[j].ID
		}
		return allBlocks[i].CreatedAt.After(allBlocks[j].CreatedAt)
	})
	for i := range allBlocks {
		b := &allBlocks[i]
		md, err := BlockToMarkdown(b)
		if err != nil {
			return nil, err
		}

		fname := safeBlockFilename(b.ID) // unique per ID, see safeBlockFilename

		outPath := filepath.Join(dir, fname)
		if err := os.WriteFile(outPath, md, 0o644); err != nil {
			return nil, fmt.Errorf("writing block file %s: %w", outPath, err)
		}
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
			stats.Summaries++
		}
	}

	return stats, nil
}

// Import reads memory block markdown files and summaries from dir.
// Import is lossless and idempotent: re-importing unchanged files changes nothing.
// If an existing block with the same ID conflicts, the version with the newer updated_at is kept,
// and the conflict is counted.
func (s *Store) Import(dir string) (*ImportStats, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("reading import directory %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("import path is not a directory: %s", dir)
	}

	stats := &ImportStats{}

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

	var incomingBlocks []*Block
	for _, fpath := range blockFiles {
		data, err := os.ReadFile(fpath)
		if err != nil {
			return nil, fmt.Errorf("reading block file %s: %w", fpath, err)
		}
		b, err := MarkdownToBlock(data)
		if err != nil {
			return nil, fmt.Errorf("parsing block file %s: %w", fpath, err)
		}
		incomingBlocks = append(incomingBlocks, b)
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

	// 1. Apply short-term blocks in-memory
	for _, b := range incomingBlocks {
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
		if existing == nil {
			s.shortTerm = append(s.shortTerm, *b)
			stats.BlocksImported++
		} else {
			if blocksEqual(existing, b) {
				stats.BlocksUnchanged++
			} else {
				stats.Conflicts++
				if b.UpdatedAt.After(existing.UpdatedAt) {
					*existing = *b
					stats.BlocksUpdated++
				} else {
					stats.BlocksUnchanged++
				}
			}
		}
	}

	// 2. Apply persisted blocks and summaries to SQLite inside writeTx
	err = s.writeTx(func(tx *sql.Tx) error {
		for _, b := range incomingBlocks {
			if b.Tier == TierShort {
				continue
			}
			existing, seq, err := getBlockTx(tx, b.ID)
			if err != nil {
				return fmt.Errorf("reading existing block %s: %w", b.ID, err)
			}
			if existing == nil {
				seq, err = insertBlockTx(tx, b)
				if err != nil {
					return err
				}
				if err := insertLexTx(tx, seq, s.lexTokens(b)); err != nil {
					return err
				}
				stats.BlocksImported++
			} else {
				if blocksEqual(existing, b) {
					stats.BlocksUnchanged++
				} else {
					stats.Conflicts++
					if b.UpdatedAt.After(existing.UpdatedAt) {
						if err := updateBlockTx(tx, b); err != nil {
							return err
						}
						if err := refreshLexTx(tx, seq, s.lexTokens(b)); err != nil {
							return err
						}
						stats.BlocksUpdated++
					} else {
						stats.BlocksUnchanged++
					}
				}
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

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("importing into memory database: %w", err)
	}

	return stats, nil
}
