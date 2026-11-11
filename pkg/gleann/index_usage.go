package gleann

import (
	"io/fs"
	"os"
	"path/filepath"
)

// IndexUsage is the disk space the indexes take: every file under the index directory (passages, vectors, graph,
// metadata of every index), and the space of the file system that holds them.
type IndexUsage struct {
	IndexDir string `json:"index_dir"`
	Indexes  int    `json:"indexes"`  // indexes with readable metadata
	Passages int    `json:"passages"` // their passages
	Bytes    int64  `json:"bytes"`    // all files under IndexDir
	// The file system of IndexDir (0 where the platform does not report it).
	DiskTotalBytes uint64 `json:"disk_total_bytes"`
	DiskFreeBytes  uint64 `json:"disk_free_bytes"` // available to gleann's user
}

// MeasureIndexUsage measures indexDir. A missing directory is empty storage, not an error.
func MeasureIndexUsage(indexDir string) (IndexUsage, error) {
	st := IndexUsage{IndexDir: indexDir}
	indexes, err := ListIndexes(indexDir)
	if err != nil {
		return st, err
	}
	st.Indexes = len(indexes)
	for _, m := range indexes {
		st.Passages += m.NumPassages
	}
	err = filepath.WalkDir(indexDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // removed while walking (an index being deleted or rebuilt)
			}
			return err
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				st.Bytes += info.Size()
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return st, err
	}
	st.DiskTotalBytes, st.DiskFreeBytes = diskSpace(indexDir)
	return st, nil
}
