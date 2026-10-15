package gleann

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// IndexHealth is the result of checking an index's passage store against its
// metadata and its vector file.
//
// The check exists because older versions edited indexes in place: removing a
// file's passages left permanent gaps in the passage IDs, while the vector
// backends address a vector by its position, so vectors ended up attached to
// the wrong passages and searches returned the wrong text. Nothing reported
// it, and an index in that state stays wrong until it is rebuilt.
type IndexHealth struct {
	Name string
	// Passages is how many passages the store holds; MaxID the highest ID.
	Passages int
	MaxID    int64
	// Dense reports whether the IDs are exactly 0..Passages-1.
	Dense bool
	// MetaPassages is the count recorded in the index metadata.
	MetaPassages int
	// Problems describes everything found wrong, one line each.
	Problems []string
	// Repairable is true when the passage store is intact, so the vector index
	// can be rebuilt from it (see LeannBuilder.Compact). It is false when the
	// store itself is missing or unreadable: the index has to be rebuilt from
	// the source files.
	Repairable bool
}

// NeedsRepair reports whether anything is wrong with the index.
func (h *IndexHealth) NeedsRepair() bool { return len(h.Problems) > 0 }

// CheckIndexHealth inspects the index called name under indexDir. It only reads.
func CheckIndexHealth(indexDir, name string) (*IndexHealth, error) {
	if err := checkPathName(name); err != nil {
		return nil, err
	}
	meta, err := GetIndexMeta(indexDir, name)
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	h := &IndexHealth{Name: name, MetaPassages: meta.NumPassages, Repairable: true}
	basePath := filepath.Join(indexDir, name, name)

	dbPath := basePath + ".passages.db"
	if _, err := os.Stat(dbPath); err != nil {
		h.Repairable = false
		h.Problems = append(h.Problems, "the passage store is missing; rebuild the index from its source files")
		return h, nil
	}
	pm := NewReadOnlyPassageManager(basePath)
	if err := pm.Load(); err != nil {
		h.Repairable = false
		h.Problems = append(h.Problems, fmt.Sprintf("the passage store cannot be read (%v); rebuild the index from its source files", err))
		return h, nil
	}
	defer pm.Close()

	var spanErr error
	h.Passages, h.MaxID, spanErr = pm.idSpan()
	if err := spanErr; err != nil {
		h.Repairable = false
		h.Problems = append(h.Problems, fmt.Sprintf("the passage store cannot be read (%v); rebuild the index from its source files", err))
		return h, nil
	}
	h.Dense = int64(h.Passages) == h.MaxID+1

	if h.Passages != meta.NumPassages {
		h.Problems = append(h.Problems, fmt.Sprintf("the metadata records %d passages but the store holds %d", meta.NumPassages, h.Passages))
	}
	// Vectors are only addressed by passage ID when there is a vector index.
	if meta.VectorReady && h.Passages > 0 {
		if !h.Dense {
			h.Problems = append(h.Problems, fmt.Sprintf(
				"passage IDs are not contiguous (%d passages, highest ID %d): vectors may be attached to the wrong passages, so searches can return the wrong text",
				h.Passages, h.MaxID))
		}
		if _, err := os.Stat(basePath + ".index"); errors.Is(err, fs.ErrNotExist) {
			h.Problems = append(h.Problems, "the vector index file is missing")
		}
	}
	return h, nil
}
