package gleann

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// LoadIndexManifest returns the file inventory of an index for change
// detection. A format-1 index reads <name>.manifest.json; a format-2 index has
// no manifest file — its files table IS the manifest (RFC 003 sections 3.4 and
// 6.1), so the entries are built from store.Files with the same Path/Size/
// ModTime/Hash shape QuickDiffDirectory expects. The content hash is the one
// committed with the passages, so a synced-but-uncommitted file never looks
// indexed.
func LoadIndexManifest(indexDir, name string) (*Manifest, error) {
	basePath := filepath.Join(indexDir, name, name)
	if StoreFormatOf(basePath) != 2 {
		return LoadManifest(basePath + ".manifest.json")
	}
	st, err := openFormat2Store(basePath)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	files, err := st.Files(context.Background())
	if err != nil {
		return nil, fmt.Errorf("reading files table: %w", err)
	}
	entries := make(map[string]ManifestEntry, len(files))
	for path, f := range files {
		entries[path] = ManifestEntry{
			Path:    f.Path,
			Size:    f.Size,
			ModTime: f.MtimeNS,
			Hash:    f.Hash,
		}
	}
	sourceDir := ""
	if m, err := GetIndexMeta(indexDir, name); err == nil {
		sourceDir = m.SourceDir
	}
	return &Manifest{
		Version:   "1.0.0",
		IndexName: name,
		SourceDir: sourceDir,
		CreatedAt: time.Now(),
		RootHash:  ComputeRootHash(entries),
		Entries:   entries,
	}, nil
}
