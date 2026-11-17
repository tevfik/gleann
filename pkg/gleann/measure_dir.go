package gleann

import (
	"io/fs"
	"os"
	"path/filepath"
)

// DirUsage is the disk space of a directory tree: the sum of its regular files and the share of each top-level
// entry (a sub-directory counts everything below it; a file counts its own size).
type DirUsage struct {
	Dir     string           `json:"dir"`
	Bytes   int64            `json:"bytes"`
	Folders map[string]int64 `json:"folders"`
}

// MeasureDir sums the regular files under dir (symlinks, sockets and the like are not followed or counted).
// A missing directory is empty, not an error; files that cannot be read or vanish during the walk are skipped.
// A dir that is itself a symlink is resolved first, because the walk does not follow its root.
func MeasureDir(dir string) (DirUsage, error) {
	out := DirUsage{Dir: dir, Folders: map[string]int64{}}
	root := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		root = resolved
	} else if os.IsNotExist(err) {
		return out, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && !os.IsNotExist(err) {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir // an unreadable directory
			}
			return nil // vanished or unreadable entry
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out.Bytes += info.Size()
		if rel, err := filepath.Rel(root, path); err == nil {
			out.Folders[topLevelEntry(rel)] += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return out, err
	}
	return out, nil
}

// topLevelEntry returns the first path element of a relative path.
func topLevelEntry(rel string) string {
	for i := 0; i < len(rel); i++ {
		if os.IsPathSeparator(rel[i]) {
			return rel[:i]
		}
	}
	return rel
}
