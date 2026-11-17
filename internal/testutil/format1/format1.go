// Package format1 writes indexes in the old (gleann v1.9, "format 1") store
// layout for tests of `gleann index migrate`, the only code that still reads
// it (RFC 004 B2/B3). Nothing outside tests imports it, and no binary *.db
// fixture is committed: every test writes the files it needs.
//
// The layout written here is the one v1.9's PassageManager produced: a bbolt
// database <name>/<name>.passages.db with a bucket "passages" whose keys are
// 8-byte big-endian passage IDs and whose values are the JSON passages
// {"id","text","metadata"}; <name>/<name>.meta.json without a store_format
// field; the vector file <name>/<name>.index as the backend wrote it; and,
// optionally, <name>/<name>.manifest.json, a <name>.lex.db sidecar and a Kùzu
// <IndexDir>/<name>_graph directory with its _graph_file_hashes.db.
package format1

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

// Passage is one stored passage. IDs need not be contiguous (an index that an
// old version edited in place has gaps).
type Passage struct {
	ID       int64          `json:"id"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Index describes the old-format index to write.
type Index struct {
	// Dir is the index directory (<IndexDir>); Name the index name.
	Dir, Name string
	Passages  []Passage
	// Meta is the meta.json content; name, version and num_passages are
	// filled in when absent. It must not carry store_format.
	Meta map[string]any
	// VectorFile is written as <name>.index when non-nil.
	VectorFile []byte
	// Manifest is written as <name>.manifest.json when non-nil.
	Manifest any
	// LexSidecar writes a placeholder <name>.lex.db (the v1.9 FTS5 sidecar).
	LexSidecar bool
	// Graph writes a placeholder Kùzu <IndexDir>/<name>_graph directory and a
	// <name>_graph_file_hashes.db next to it.
	Graph bool
}

// Write writes ix. It fails if the index directory already holds a format-2
// database.
func Write(ix Index) error {
	idxDir := filepath.Join(ix.Dir, ix.Name)
	base := filepath.Join(idxDir, ix.Name)
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(base + ".db"); err == nil {
		return fmt.Errorf("format1: %s.db exists; refusing to mix layouts", base)
	}
	db, err := bbolt.Open(base+".passages.db", 0o644, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("format1: open bbolt: %w", err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("passages"))
		if err != nil {
			return err
		}
		var maxID int64 = -1
		for _, p := range ix.Passages {
			data, err := json.Marshal(p)
			if err != nil {
				return err
			}
			var key [8]byte
			binary.BigEndian.PutUint64(key[:], uint64(p.ID))
			if err := b.Put(key[:], data); err != nil {
				return err
			}
			maxID = max(maxID, p.ID)
		}
		// The sequence v1.9 used to hand out the next ID.
		return b.SetSequence(uint64(maxID + 1))
	})
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("format1: write passages: %w", err)
	}

	meta := map[string]any{}
	for k, v := range ix.Meta {
		meta[k] = v
	}
	if _, ok := meta["store_format"]; ok {
		return fmt.Errorf("format1: meta must not carry store_format")
	}
	setDefault := func(k string, v any) {
		if _, ok := meta[k]; !ok {
			meta[k] = v
		}
	}
	setDefault("name", ix.Name)
	setDefault("version", "1.0.0")
	setDefault("gleann_version", "1.9.0")
	setDefault("num_passages", len(ix.Passages))
	setDefault("created_at", time.Now().UTC())
	setDefault("updated_at", time.Now().UTC())
	if err := writeJSON(base+".meta.json", meta); err != nil {
		return err
	}
	if ix.VectorFile != nil {
		if err := os.WriteFile(base+".index", ix.VectorFile, 0o644); err != nil {
			return err
		}
	}
	if ix.Manifest != nil {
		if err := writeJSON(base+".manifest.json", ix.Manifest); err != nil {
			return err
		}
	}
	if ix.LexSidecar {
		if err := os.WriteFile(base+".lex.db", []byte("v1.9 lexical sidecar placeholder"), 0o644); err != nil {
			return err
		}
	}
	if ix.Graph {
		g := filepath.Join(ix.Dir, ix.Name+"_graph")
		if err := os.MkdirAll(g, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(g, "catalog.kz"), []byte("kuzu placeholder"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(ix.Dir, ix.Name+"_graph_file_hashes.db"), []byte("hash store placeholder"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
