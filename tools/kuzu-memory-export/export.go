// Package main is kuzu-memory-export: it reads a Memory Engine store written
// by gleann v1.9.x (a Kùzu database at <IndexDir>/<name>_memory) and writes
// its entities, relations and archive rows to a JSON file that
// `gleann memory import-graph` loads into memory.sqlite.
//
// It lives in its own module so the Kùzu binding (CGo) stays out of gleann.
// It never writes to the user's files: a store that cannot be opened
// read-only (for example because of a pending write-ahead log) is copied to a
// temp directory and the copy is opened instead.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	kuzu "github.com/kuzudb/go-kuzu"
)

// Export format identification. Keep in sync with pkg/memory/graphimport.go in
// gleann (the importer validates both fields).
const (
	FormatName    = "gleann-kuzu-memory-export"
	FormatVersion = 1
)

// Entity is one row of the Entity node table.
type Entity struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	Attributes string `json:"attributes"` // JSON object text exactly as stored
}

// Relation is one RELATES_TO edge. Timestamps (created_at/updated_at) live
// inside Attributes, as v1.9 stored them.
type Relation struct {
	From         string  `json:"from"`
	To           string  `json:"to"`
	RelationType string  `json:"relation_type"`
	Weight       float64 `json:"weight"`
	Attributes   string  `json:"attributes"`
}

// ArchiveRow is one Entity_Archive row (a previous version of an entity).
type ArchiveRow struct {
	ArchiveID  string `json:"archive_id"`
	ID         string `json:"id"`
	VersionID  string `json:"version_id"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	Attributes string `json:"attributes"`
	ValidTo    string `json:"valid_to"`
}

// Export is the file format.
type Export struct {
	Format    string       `json:"format"`
	Version   int          `json:"version"`
	Namespace string       `json:"namespace"`
	Entities  []Entity     `json:"entities"`
	Relations []Relation   `json:"relations"`
	Archive   []ArchiveRow `json:"archive"`
}

// NamespaceOf derives the index name from a store path (<name>_memory).
func NamespaceOf(storePath string) string {
	base := filepath.Base(filepath.Clean(storePath))
	return strings.TrimSuffix(base, "_memory")
}

func isStorePath(p string) bool {
	return strings.HasSuffix(filepath.Clean(p), "_memory")
}

// DiscoverStores lists the <name>_memory stores (files or directories) in dir;
// when dir has none it also looks in dir/indexes.
func DiscoverStores(dir string) ([]string, error) {
	for _, d := range []string{dir, filepath.Join(dir, "indexes")} {
		entries, err := os.ReadDir(d)
		if err != nil {
			if d == dir {
				return nil, fmt.Errorf("reading %s: %w", d, err)
			}
			continue
		}
		var out []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_memory") {
				out = append(out, filepath.Join(d, e.Name()))
			}
		}
		if len(out) > 0 {
			sort.Strings(out)
			return out, nil
		}
	}
	return nil, nil
}

// ExportStore reads the store at path. It never modifies it.
func ExportStore(path string) (*Export, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	ex, err := exportFrom(path, true, NamespaceOf(path))
	if err == nil {
		return ex, nil
	}
	// A leftover write-ahead log (or a held file) can make a read-only open
	// fail. Work on a copy so the user's files stay untouched.
	tmp, cerr := os.MkdirTemp("", "kuzu-memory-export-*")
	if cerr != nil {
		return nil, fmt.Errorf("opening %s read-only: %v; temp dir: %w", path, err, cerr)
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, filepath.Base(path))
	if cerr := copyStore(path, cp); cerr != nil {
		return nil, fmt.Errorf("opening %s read-only: %v; copying it: %w", path, err, cerr)
	}
	ex, err2 := exportFrom(cp, false, NamespaceOf(path))
	if err2 != nil {
		return nil, fmt.Errorf("opening %s: read-only: %v; on a copy: %w", path, err, err2)
	}
	return ex, nil
}

func exportFrom(path string, readOnly bool, ns string) (*Export, error) {
	cfg := kuzu.DefaultSystemConfig()
	cfg.ReadOnly = readOnly
	db, err := kuzu.OpenDatabase(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("kuzu open: %w", err)
	}
	defer db.Close()
	conn, err := kuzu.OpenConnection(db)
	if err != nil {
		return nil, fmt.Errorf("kuzu connection: %w", err)
	}
	defer conn.Close()

	tables, err := tableNames(conn)
	if err != nil {
		return nil, err
	}
	ex := &Export{Format: FormatName, Version: FormatVersion, Namespace: ns,
		Entities: []Entity{}, Relations: []Relation{}, Archive: []ArchiveRow{}}
	if !tables["Entity"] {
		return nil, fmt.Errorf("%s has no Entity table: not a gleann memory store", path)
	}
	rows, err := queryMaps(conn, `MATCH (e:Entity) RETURN e.id AS id, e.type AS type, e.content AS content, e.attributes AS attributes`)
	if err != nil {
		return nil, fmt.Errorf("reading entities: %w", err)
	}
	for _, m := range rows {
		ex.Entities = append(ex.Entities, Entity{ID: str(m["id"]), Type: str(m["type"]),
			Content: str(m["content"]), Attributes: attrs(m["attributes"])})
	}
	if tables["RELATES_TO"] {
		rows, err = queryMaps(conn, `MATCH (a:Entity)-[r:RELATES_TO]->(b:Entity)
RETURN a.id AS src, b.id AS dst, r.relation_type AS rt, r.weight AS weight, r.attributes AS attributes`)
		if err != nil {
			return nil, fmt.Errorf("reading relations: %w", err)
		}
		for _, m := range rows {
			w := 1.0
			if v, ok := m["weight"].(float64); ok {
				w = v
			}
			ex.Relations = append(ex.Relations, Relation{From: str(m["src"]), To: str(m["dst"]),
				RelationType: str(m["rt"]), Weight: w, Attributes: attrs(m["attributes"])})
		}
	}
	// Stores created before the temporal archive existed lack this table.
	if tables["Entity_Archive"] {
		rows, err = queryMaps(conn, `MATCH (a:Entity_Archive) RETURN a.archive_id AS aid, a.id AS id, a.version_id AS vid,
a.type AS type, a.content AS content, a.attributes AS attributes, a.valid_to AS valid_to`)
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		for _, m := range rows {
			ex.Archive = append(ex.Archive, ArchiveRow{ArchiveID: str(m["aid"]), ID: str(m["id"]),
				VersionID: str(m["vid"]), Type: str(m["type"]), Content: str(m["content"]),
				Attributes: attrs(m["attributes"]), ValidTo: str(m["valid_to"])})
		}
	}
	// Stable output so two exports of one store are byte-identical.
	sort.Slice(ex.Entities, func(i, j int) bool { return ex.Entities[i].ID < ex.Entities[j].ID })
	sort.Slice(ex.Relations, func(i, j int) bool {
		a, b := ex.Relations[i], ex.Relations[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.RelationType < b.RelationType
	})
	sort.Slice(ex.Archive, func(i, j int) bool { return ex.Archive[i].ArchiveID < ex.Archive[j].ArchiveID })
	return ex, nil
}

func tableNames(conn *kuzu.Connection) (map[string]bool, error) {
	rows, err := queryMaps(conn, `CALL show_tables() RETURN *`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	out := map[string]bool{}
	for _, m := range rows {
		out[str(m["name"])] = true
	}
	return out, nil
}

func queryMaps(conn *kuzu.Connection, q string) ([]map[string]any, error) {
	res, err := conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var out []map[string]any
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// attrs returns the stored attribute JSON, "{}" when NULL or empty.
func attrs(v any) string {
	if s := str(v); s != "" {
		return s
	}
	return "{}"
}

// copyStore copies a store file (plus its .wal/.shadow) or directory.
func copyStore(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return copyDir(src, dst)
	}
	for _, suf := range []string{"", ".wal", ".shadow"} {
		if err := copyFile(src+suf, dst+suf); err != nil && (suf == "" || !os.IsNotExist(err)) {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		return copyFile(p, t)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// WriteExport writes ex as <dir>/<namespace>.kuzu-export.json.
func WriteExport(ex *Export, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, ex.Namespace+".kuzu-export.json")
	return p, os.WriteFile(p, append(data, '\n'), 0o644)
}
