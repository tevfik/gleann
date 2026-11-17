package gleann

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/store"
)

// LexicalQueryOptions returns the query options of the FTS5 lexical leg:
// AND-first query construction (RFC 003 step 2) and a document-frequency cut
// of 0.2 (step 3). They are fixed since v1.20 (RFC 004 B1); the former
// GLEANN_LEXICAL_OPTS switch is gone.
func LexicalQueryOptions() store.LexQueryOptions {
	return store.LexQueryOptions{ANDFirst: true, DocFreqCut: 0.2}
}

// NoticeRemovedEnv writes one line to w when an environment variable that
// v1.20 no longer reads is still set (RFC 004 B1: format 2 and the FTS5
// lexical leg with AND-first and a document-frequency cut are fixed); the
// values are ignored. It writes nothing otherwise.
func NoticeRemovedEnv(w io.Writer) {
	var set []string
	for _, k := range []string{"GLEANN_STORE_FORMAT", "GLEANN_LEXICAL", "GLEANN_LEXICAL_OPTS"} {
		if _, ok := os.LookupEnv(k); ok {
			set = append(set, k)
		}
	}
	if len(set) == 0 {
		return
	}
	fmt.Fprintf(w, "notice: %s ignored: removed in gleann v1.20 (every index is format 2 with FTS5 lexical search)\n", strings.Join(set, ", "))
}

// passageKind selects the analyzer kind from the source file type: prose for
// documents, code for everything else (RFC 003 section 4.1 item 3).
func passageKind(source string) string {
	switch strings.ToLower(filepath.Ext(source)) {
	case ".md", ".mdx", ".txt", ".rst", ".adoc", ".org":
		return "prose"
	default:
		return "code"
	}
}

// removeDBFiles removes a SQLite database and its WAL companions and reports
// the first failure other than a missing file. A caller that recreates the
// database must use it: on Windows deleting a database another process holds
// open fails, and opening the path afterwards would silently reuse the old
// database instead of starting from a clean schema.
func removeDBFiles(path string) error {
	var first error
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}
