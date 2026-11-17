//go:build windows

package diskann

import (
	"fmt"
	"io"
	"os"
)

// releaseFileAfterLoad is true on Windows: the reader loads a private heap
// copy of the index file and closes it at once, instead of mapping it.
//
// Windows refuses to replace or delete a file while a process holds it open
// without FILE_SHARE_DELETE (os.Open does not set it) or while a view of it is
// mapped (ERROR_USER_MAPPED_FILE). A publication renames <name>.index.tmp over
// <name>.index (pkg/gleann publishVectorFormat2) while a long-lived reader,
// such as the server's searcher cache, may still be serving the previous
// generation, so a mapped or open main file would make every incremental
// update and merge fail there. The trade-off: on Windows each open index costs
// its file size in process memory and is read in full at open, where Unix
// maps it lazily and shares the pages with the page cache.
const releaseFileAfterLoad = true

// mmapFile returns a heap copy of the first size bytes of f; see
// releaseFileAfterLoad.
func mmapFile(f *os.File, size int64) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, size), data); err != nil {
		return nil, fmt.Errorf("reading index file: %w", err)
	}
	return data, nil
}

// munmapFile is a no-op: the data is ordinary Go memory (see mmapFile).
func munmapFile([]byte) error { return nil }
