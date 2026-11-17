package gleann

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tevfik/gleann/internal/store"
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

	if StoreFormatOf(basePath) == 2 {
		checkHealthFormat2(h, meta, basePath)
	} else {
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
	}

	// Check graph database if present on disk.
	graphDir := filepath.Join(indexDir, name+"_graph")
	if info, err := os.Stat(graphDir); err == nil && (info.IsDir() || info.Size() > 0) {
		if GraphDBOpener != nil {
			if db, err := GraphDBOpener(graphDir); err != nil {
				h.Problems = append(h.Problems, fmt.Sprintf("the graph database cannot be opened (%v)", err))
			} else {
				db.Close()
			}
		}
	}

	return h, nil
}

// checkHealthFormat2 fills h for a format-2 index (RFC 003 sections 3.4 and
// 6.3): the vector-slot span replaces the bbolt ID span, and the format
// recorded in meta.json is cross-checked against the files on disk and the
// database's own meta table. It also reports an interrupted vector
// publication (the pending keys) and a generation mismatch between meta.json
// and the database; the next open's recovery or a repair resolves both.
func checkHealthFormat2(h *IndexHealth, meta *IndexMeta, basePath string) {
	if meta.StoreFormat != 2 {
		h.Problems = append(h.Problems, "the index files are store format 2 but meta.json does not record store_format 2; repair the index to restore the field")
	}
	// Stat before opening: store.Open would create a missing database, and a
	// health check must not write.
	if _, err := os.Stat(basePath + ".db"); err != nil {
		h.Repairable = false
		h.Problems = append(h.Problems, "the passage store is missing; rebuild the index from its source files")
		return
	}
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		h.Repairable = false
		h.Problems = append(h.Problems, fmt.Sprintf("the passage store cannot be read (%v); rebuild the index from its source files", err))
		return
	}
	defer st.Close()
	ctx := context.Background()
	if f, err := st.Meta(ctx, "store_format"); err == nil && f != "2" {
		h.Problems = append(h.Problems, fmt.Sprintf("the store's own meta table records format %q, expected 2", f))
	}
	// An interrupted vector publication leaves pending keys behind. The next
	// open for search or write rolls it forward (recoverVectorPublication);
	// doctor reports the state it sees, with the repair that also resolves it.
	if pf, err := st.Meta(ctx, "pending_vector_file"); err == nil && pf != "" {
		h.Problems = append(h.Problems, fmt.Sprintf(
			"a vector publication is pending (%s): a crash interrupted it; reopen the index for search to roll it forward, or repair it with: gleann index repair %s",
			pf, h.Name))
	}
	// The published generation is mirrored in meta.json after each
	// publication; a mismatch means a crash landed between the database
	// finish and the metadata write (RFC 003 section 3.4).
	dbGen := 0
	if v, err := st.Meta(ctx, "vector_generation"); err == nil && v != "" {
		dbGen, _ = strconv.Atoi(v)
	}
	if meta.VectorGeneration != dbGen {
		h.Problems = append(h.Problems, fmt.Sprintf(
			"meta.json records vector generation %d but the store records %d: a crash interrupted a vector publication; repair the index with: gleann index repair %s",
			meta.VectorGeneration, dbGen, h.Name))
	}
	count, maxSlot, slotless, err := st.SlotSpan(ctx)
	if err != nil {
		h.Repairable = false
		h.Problems = append(h.Problems, fmt.Sprintf("the passage store cannot be read (%v); rebuild the index from its source files", err))
		return
	}
	h.Passages, h.MaxID = count, maxSlot
	h.Dense = slotless == 0 && int64(count) == maxSlot+1

	if h.Passages != meta.NumPassages {
		h.Problems = append(h.Problems, fmt.Sprintf("the metadata records %d passages but the store holds %d", meta.NumPassages, h.Passages))
	}
	// Vectors are only addressed by slot when there is a vector index.
	if meta.VectorReady && h.Passages > 0 {
		if !h.Dense {
			h.Problems = append(h.Problems, fmt.Sprintf(
				"vector slots are not contiguous (%d passages, highest slot %d, %d without a slot): vectors may be attached to the wrong passages, so searches can return the wrong text",
				h.Passages, h.MaxID, slotless))
		}
		if _, err := os.Stat(basePath + ".index"); errors.Is(err, fs.ErrNotExist) {
			h.Problems = append(h.Problems, "the vector index file is missing")
		}
	}
}
