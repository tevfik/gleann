package memory

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Format of the files written by tools/kuzu-memory-export (RFC 004 item C).
// The exporter and this reader are versioned together; a file with another
// format name or a newer version is rejected, never guessed at.
const (
	GraphExportFormat  = "gleann-kuzu-memory-export"
	GraphExportVersion = 1

	graphImportMetaPrefix = "graph_import:"
)

// GraphExport is the content of one export file: everything a v1.9.x Memory
// Engine store (<IndexDir>/<name>_memory) held for one namespace.
type GraphExport struct {
	Format    string               `json:"format"`
	Version   int                  `json:"version"`
	Namespace string               `json:"namespace"`
	Entities  []GraphExportEntity  `json:"entities"`
	Relations []GraphExportRel     `json:"relations"`
	Archive   []GraphExportArchive `json:"archive"`
}

// GraphExportEntity is one active entity; Attributes is the stored JSON text.
type GraphExportEntity struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	Attributes string `json:"attributes"`
}

// GraphExportRel is one directed relation; created_at/updated_at are inside Attributes.
type GraphExportRel struct {
	From         string  `json:"from"`
	To           string  `json:"to"`
	RelationType string  `json:"relation_type"`
	Weight       float64 `json:"weight"`
	Attributes   string  `json:"attributes"`
}

// GraphExportArchive is one earlier version of an entity.
type GraphExportArchive struct {
	ArchiveID  string `json:"archive_id"`
	ID         string `json:"id"`
	VersionID  string `json:"version_id"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	Attributes string `json:"attributes"`
	ValidTo    string `json:"valid_to"`
}

// GraphImportResult reports what one namespace import did.
type GraphImportResult struct {
	Namespace string
	Source    string
	// AlreadyImported is true when the marker records the same file hash:
	// nothing was read or written.
	AlreadyImported bool
	// Rows added; rows already present are left as they are.
	Entities, Relations, Archive int
	// AddedEntities are the entities this import inserted (not the ones that
	// were already present), for the memory vector index.
	AddedEntities []GraphExportEntity
}

type graphImportMarker struct {
	Source     string `json:"source"`
	SHA256     string `json:"sha256"`
	ImportedAt string `json:"imported_at"`
	Entities   int    `json:"entities"`
	Relations  int    `json:"relations"`
	Archive    int    `json:"archive"`
}

// ParseGraphExport decodes and validates an export file.
func ParseGraphExport(data []byte) (*GraphExport, error) {
	var ex GraphExport
	if err := json.Unmarshal(data, &ex); err != nil {
		return nil, fmt.Errorf("not a gleann memory export (invalid JSON): %w", err)
	}
	if ex.Format != GraphExportFormat {
		return nil, fmt.Errorf("not a gleann memory export: format %q, want %q (create it with kuzu-memory-export)", ex.Format, GraphExportFormat)
	}
	if ex.Version != GraphExportVersion {
		return nil, fmt.Errorf("unsupported export version %d (this gleann reads version %d)", ex.Version, GraphExportVersion)
	}
	if err := gleann.ValidateName(ex.Namespace); err != nil {
		return nil, fmt.Errorf("export namespace %q: %w", ex.Namespace, err)
	}
	for i, e := range ex.Entities {
		if e.ID == "" {
			return nil, fmt.Errorf("entity %d has no id", i)
		}
		if err := checkAttrs(e.Attributes); err != nil {
			return nil, fmt.Errorf("entity %q attributes: %w", e.ID, err)
		}
	}
	for i, r := range ex.Relations {
		if r.From == "" || r.To == "" {
			return nil, fmt.Errorf("relation %d has an empty endpoint", i)
		}
		if err := checkAttrs(r.Attributes); err != nil {
			return nil, fmt.Errorf("relation %q->%q attributes: %w", r.From, r.To, err)
		}
	}
	for i, a := range ex.Archive {
		if a.ArchiveID == "" || a.ID == "" {
			return nil, fmt.Errorf("archive row %d has no archive_id or id", i)
		}
		if err := checkAttrs(a.Attributes); err != nil {
			return nil, fmt.Errorf("archive row %q attributes: %w", a.ArchiveID, err)
		}
	}
	return &ex, nil
}

func checkAttrs(s string) error {
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return fmt.Errorf("not a JSON object: %w", err)
	}
	return nil
}

func attrsOrEmpty(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

// GraphImportMarker returns the recorded import of ns ("" when never imported).
func (s *Store) GraphImportMarker(ns string) (string, error) {
	return s.metaValue(graphImportMetaPrefix + ns)
}

// ImportGraphFile imports one export file (see importGraphExport).
func (s *Store) ImportGraphFile(path string) (GraphImportResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GraphImportResult{}, fmt.Errorf("reading %s: %w", path, err)
	}
	ex, err := ParseGraphExport(data)
	if err != nil {
		return GraphImportResult{}, fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	abs, aerr := filepath.Abs(path)
	if aerr != nil {
		abs = path
	}
	return s.importGraphExport(ex, abs, hex.EncodeToString(sum[:]))
}

// ImportGraphPath imports a file or every *.json file of a directory, in name
// order; an error stops at the failing file.
func (s *Store) ImportGraphPath(path string) ([]GraphImportResult, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("import-graph: %w", err)
	}
	if !fi.IsDir() {
		r, err := s.ImportGraphFile(path)
		if err != nil {
			return nil, err
		}
		return []GraphImportResult{r}, nil
	}
	matches, err := filepath.Glob(filepath.Join(path, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", path, err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("import-graph: no *.json export files in %s", path)
	}
	var out []GraphImportResult
	for _, m := range matches {
		r, err := s.ImportGraphFile(m)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// importGraphExport loads ex into the SQLite tables in one transaction with
// INSERT OR IGNORE, keeping ids, attributes and timestamps as exported
// (EntityService.InjectEntities is not used: it would re-archive and
// re-stamp). A namespace whose marker records the same file hash is left
// untouched. Rows that already exist in SQLite win over the export.
//
// archive_id is the table's key across every namespace, so imported archive
// rows are stored as "<ns>/<archive_id>", the form EntityService uses.
func (s *Store) importGraphExport(ex *GraphExport, source, hash string) (GraphImportResult, error) {
	res := GraphImportResult{Namespace: ex.Namespace, Source: source}
	key := graphImportMetaPrefix + ex.Namespace
	raw, err := s.metaValue(key)
	if err != nil {
		return res, err
	}
	if raw != "" {
		var m graphImportMarker
		if json.Unmarshal([]byte(raw), &m) == nil && m.SHA256 == hash {
			res.AlreadyImported = true
			return res, nil
		}
	}

	err = s.writeTx(func(tx *sql.Tx) error {
		var err error
		res.AddedEntities = nil
		res.Entities, err = insertAll(len(ex.Entities), func(i int) (sql.Result, error) {
			e := ex.Entities[i]
			r, err := tx.Exec(`INSERT OR IGNORE INTO entities (ns, id, type, content, attributes) VALUES (?, ?, ?, ?, ?)`,
				ex.Namespace, e.ID, e.Type, e.Content, attrsOrEmpty(e.Attributes))
			if err == nil {
				if k, kerr := r.RowsAffected(); kerr == nil && k > 0 {
					res.AddedEntities = append(res.AddedEntities, e)
				}
			}
			return r, err
		})
		if err != nil {
			return fmt.Errorf("importing entities: %w", err)
		}
		res.Relations, err = insertAll(len(ex.Relations), func(i int) (sql.Result, error) {
			r := ex.Relations[i]
			return tx.Exec(`INSERT OR IGNORE INTO relations (ns, src, dst, relation_type, weight, attributes) VALUES (?, ?, ?, ?, ?, ?)`,
				ex.Namespace, r.From, r.To, r.RelationType, r.Weight, attrsOrEmpty(r.Attributes))
		})
		if err != nil {
			return fmt.Errorf("importing relations: %w", err)
		}
		res.Archive, err = insertAll(len(ex.Archive), func(i int) (sql.Result, error) {
			a := ex.Archive[i]
			return tx.Exec(`INSERT OR IGNORE INTO entity_archive (archive_id, ns, id, version_id, type, content, attributes, valid_to) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				ex.Namespace+"/"+a.ArchiveID, ex.Namespace, a.ID, a.VersionID, a.Type, a.Content, attrsOrEmpty(a.Attributes), a.ValidTo)
		})
		if err != nil {
			return fmt.Errorf("importing archive: %w", err)
		}

		marker, err := json.Marshal(graphImportMarker{Source: source, SHA256: hash,
			ImportedAt: time.Now().UTC().Format(time.RFC3339),
			Entities:   len(ex.Entities), Relations: len(ex.Relations), Archive: len(ex.Archive)})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(marker))
		return err
	})
	if err != nil {
		return res, fmt.Errorf("import-graph namespace %q: %w", ex.Namespace, err)
	}
	return res, nil
}

func insertAll(n int, do func(i int) (sql.Result, error)) (int, error) {
	inserted := 0
	for i := 0; i < n; i++ {
		r, err := do(i)
		if err != nil {
			return inserted, err
		}
		if k, err := r.RowsAffected(); err == nil {
			inserted += int(k)
		}
	}
	return inserted, nil
}

// ── Detection of old Kùzu stores ─────────────────────────────────────────────

// LegacyKuzuStore is a v1.9.x entity store found next to the indexes.
type LegacyKuzuStore struct {
	Name string // index name
	Path string // <IndexDir>/<name>_memory
}

// LegacyKuzuStorePath returns <indexDir>/<name>_memory when it exists as a
// Kùzu store: a non-empty file (go-kuzu 0.11) or a directory (older layouts)
// that is not itself an index (indexes have a meta.json).
func LegacyKuzuStorePath(indexDir, name string) (string, bool) {
	p := filepath.Join(indexDir, name+"_memory")
	fi, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		if _, err := os.Stat(filepath.Join(p, "meta.json")); err == nil {
			return "", false // an index that happens to be named <name>_memory
		}
		return p, true
	}
	return p, fi.Size() > 0
}

// ScanLegacyKuzuStores lists every <name>_memory Kùzu store in indexDir,
// imported or not (no memory database needed).
func ScanLegacyKuzuStores(indexDir string) []LegacyKuzuStore {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return nil
	}
	var out []LegacyKuzuStore
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), "_memory")
		if !ok || name == "" {
			continue
		}
		if p, ok := LegacyKuzuStorePath(indexDir, name); ok {
			out = append(out, LegacyKuzuStore{Name: name, Path: p})
		}
	}
	return out
}

// LegacyKuzuStores lists the old stores in indexDir that have no import marker.
func (s *Store) LegacyKuzuStores(indexDir string) []LegacyKuzuStore {
	var out []LegacyKuzuStore
	for _, l := range ScanLegacyKuzuStores(indexDir) {
		if m, err := s.GraphImportMarker(l.Name); err == nil && m != "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// LegacyImportCommands returns the two commands that carry a store over.
func LegacyImportCommands(l LegacyKuzuStore) (export, importCmd string) {
	return fmt.Sprintf("kuzu-memory-export %q -o gleann-memory-export", l.Path),
		fmt.Sprintf("gleann memory import-graph %q", filepath.Join("gleann-memory-export", l.Name+".kuzu-export.json"))
}

// WarnLegacyKuzuOnce logs, once per process and index name, the two commands
// that carry an old <name>_memory store over, when one exists in indexDir
// and was not imported yet.
func (s *Store) WarnLegacyKuzuOnce(indexDir, name string) {
	p, ok := LegacyKuzuStorePath(indexDir, name)
	if !ok {
		return
	}
	if m, err := s.GraphImportMarker(name); err != nil || m != "" {
		return
	}
	if _, dup := s.legacyWarned.LoadOrStore(name, true); dup {
		return
	}
	exp, imp := LegacyImportCommands(LegacyKuzuStore{Name: name, Path: p})
	slog.Warn("entities of gleann v1.9 found in an old Kùzu store and not imported; run: "+exp+" ; then: "+imp,
		"index", name, "path", p)
}
