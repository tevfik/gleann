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
// of 0.2 (step 3) in indexes of at least DocFreqCutMinRows passages. They are
// fixed since v1.20 (RFC 004 B1); the former GLEANN_LEXICAL_OPTS switch is
// gone.
func LexicalQueryOptions() store.LexQueryOptions {
	return store.LexQueryOptions{ANDFirst: true, DocFreqCut: 0.2, DocFreqCutMinRows: DocFreqCutMinRows}
}

// DocFreqCutMinRows is the index size from which the document-frequency cut
// applies. Below it the cut cost more than it gained: in an index of one
// document it dropped the document's own subject words, which questions name
// (18 PDFs, one index each, 34 questions: the cut lost 1 to 3 answers that
// reached the LLM without it), while a query without the cut stays fast (synthetic
// corpus, p95: 20 ms at 10K passages, 40 ms at 20K; 200 ms at 100K, where
// the cut brings it to 22 ms).
const DocFreqCutMinRows = 10_000

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
// documents (including those a plugin converts to text), code for everything
// else (RFC 003 section 4.1 item 3).
func passageKind(source string) string {
	switch strings.ToLower(filepath.Ext(source)) {
	case ".md", ".mdx", ".txt", ".rst", ".adoc", ".org",
		".pdf", ".docx", ".doc", ".odt", ".rtf", ".html", ".htm", ".epub", ".pptx":
		return "prose"
	default:
		return "code"
	}
}

// guessLanguage infers the analyzer language of an index from its corpus: when
// at least a fifth of the prose passages contain letters that only Turkish
// uses (ı, İ, ş, ğ), the index is Turkish ("tr") and its prose gets the
// Turkish rules (dotted and dotless I, ASCII-folded variants); otherwise the
// default rules apply (RFC 003 section 6.4 step 2). n passages are read
// through at, which returns a passage's source path and text. Every full
// build decides it again; incremental updates keep the stored language.
func guessLanguage(n int, at func(i int) (source, text string)) string {
	prose, tr := 0, 0
	for i := 0; i < n; i++ {
		src, text := at(i)
		if passageKind(src) != "prose" {
			continue
		}
		prose++
		if strings.ContainsAny(text, "ıİşğŞĞ") {
			tr++
		}
	}
	if prose > 0 && tr*5 >= prose {
		return "tr"
	}
	return ""
}

// itemsLanguage is guessLanguage over the items of a build.
func itemsLanguage(items []Item) string {
	return guessLanguage(len(items), func(i int) (string, string) {
		return itemSource(items[i].Metadata), items[i].Text
	})
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
