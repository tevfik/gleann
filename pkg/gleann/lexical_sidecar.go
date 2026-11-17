package gleann

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// sidecarLanguage is the analyzer language used for the lexical sidecar in
// this step. There is no per-index language setting yet; RFC 003 step 4 adds
// meta.language and reads it here instead of this constant.
const sidecarLanguage = ""

// lexicalSidecarPath returns the sidecar database path
// <IndexDir>/<name>/<name>.lex.db. The name is deliberately not <name>.db:
// that name is reserved for the format-2 record store of RFC 003 step 4. The
// sidecar lives inside the index directory, so RemoveIndex's RemoveAll of the
// directory deletes it (and its -wal/-shm companions) with everything else.
func lexicalSidecarPath(indexDir, name string) string {
	return filepath.Join(indexDir, name, name+".lex.db")
}

// openFTSScorer opens the persisted lexical sidecar for the loaded index,
// rebuilding it first when it is missing or stale. It is called from Load only
// when GLEANN_LEXICAL=fts5 and the scorer is the default BM25 adapter.
//
// It returns nil, with a log line, when the sidecar cannot be made fresh: a
// read-only file system, another process rebuilding it right now (its lock
// is held), or any build error. Load then keeps the in-RAM BM25 adapter, so a
// search never fails and never reads a stale sidecar because of the switch.
func (s *LeannSearcher) openFTSScorer(ctx context.Context, basePath string) *FTSScorer {
	lexPath := basePath + ".lex.db"
	analyzer := analysis.New(sidecarLanguage)
	if !s.sidecarFresh(ctx, lexPath, analyzer) {
		if err := buildLexicalSidecar(ctx, s.config.IndexDir, s.meta.Name); err != nil {
			log.Printf("ℹ️  Persisted lexical index for %q not available (%v); using in-memory BM25 for this load", s.meta.Name, err)
			return nil
		}
	}
	st, err := store.Open(lexPath, store.Options{})
	if err != nil {
		log.Printf("ℹ️  Opening persisted lexical index %s failed (%v); using in-memory BM25 for this load", lexPath, err)
		return nil
	}
	opts := parseLexicalOpts(os.Getenv("GLEANN_LEXICAL_OPTS"))
	return &FTSScorer{store: st, analyzer: analyzer, opts: opts}
}

// parseLexicalOpts parses the GLEANN_LEXICAL_OPTS environment variable into
// store.LexQueryOptions. Options are comma-separated:
// - "and", "and-first", "and_first" enables AND-first query construction (RFC 003 step 2)
// - "df=<fraction>" (e.g. "df=0.05", "df=0.1", "df=0.2") sets document frequency cut (step 3)
func parseLexicalOpts(raw string) store.LexQueryOptions {
	var opts store.LexQueryOptions
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		switch {
		case part == "and" || part == "and-first" || part == "and_first":
			opts.ANDFirst = true
		case strings.HasPrefix(part, "df="):
			if v, err := strconv.ParseFloat(strings.TrimPrefix(part, "df="), 64); err == nil {
				opts.DocFreqCut = v
			}
		}
	}
	return opts
}

// sidecarFresh reports whether the sidecar can be used as is: it exists, its
// analyzer identity and language match the running ones, and its passage count
// equals the index's bbolt passage count. Any mismatch means rebuild. All
// handles are closed before returning so a later rename is safe on Windows.
func (s *LeannSearcher) sidecarFresh(ctx context.Context, lexPath string, analyzer analysis.Analyzer) bool {
	if _, err := os.Stat(lexPath); err != nil {
		return false // missing
	}
	st, err := store.Open(lexPath, store.Options{})
	if err != nil {
		return false // unreadable or corrupt: rebuild
	}
	defer st.Close()
	if an, err := st.Meta(ctx, "analyzer"); err != nil || an != analysis.ID {
		return false
	}
	if lang, err := st.Meta(ctx, "language"); err != nil || lang != analyzer.Language {
		return false
	}
	cnt, err := st.Meta(ctx, "passage_count")
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(cnt)
	if err != nil || n != s.passages.Count() {
		return false
	}
	// The count alone misses an edit that keeps it (a changed file with the
	// same number of chunks, synced while the switch was off). Every passage
	// write changes the bbolt file's size or modification time.
	if v, err := st.Meta(ctx, "passages_file"); err != nil || v != passagesFileStamp(s.config.IndexDir, s.meta.Name) {
		return false
	}
	return true
}

// passagesFileStamp identifies the current content of the bbolt passage
// store by size and modification time; "" when the file cannot be read.
func passagesFileStamp(indexDir, name string) string {
	fi, err := os.Stat(filepath.Join(indexDir, name, name+".passages.db"))
	if err != nil {
		return ""
	}
	return strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

// errSidecarBusy reports that another process is rebuilding the sidecar.
var errSidecarBusy = errors.New("another process is rebuilding the persisted lexical index")

// lexicalLockPath is the lock that serialises sidecar rebuilds across
// processes: <IndexDir>/<name>.lex.lock, beside the index directory so a
// rename of the directory is never blocked by it (RFC 003 section 3.6).
func lexicalLockPath(indexDir, name string) string {
	return filepath.Join(indexDir, name+".lex.lock")
}

// buildLexicalSidecar rebuilds <name>.lex.db from the bbolt passages, which
// stay the record store in this step. The rebuild happens in place, in one
// transaction (store.ReplaceAll): a searcher in another process that has the
// sidecar open keeps reading the previous committed state until the commit
// and then sees the new one. Files are never swapped, because renaming over
// an open database fails on Windows and strands the reader on unix.
//
// Rebuilds are serialised by an OS lock; when another process holds it this
// returns errSidecarBusy at once instead of waiting. Passages are streamed
// from bbolt, so memory does not grow with the corpus. The lex rowid equals
// the bbolt passage ID (vec_slot too, RFC 003 section 6.4 step 2), so TopK
// returns IDs the searcher already understands.
func buildLexicalSidecar(ctx context.Context, indexDir, name string) error {
	lock, err := store.AcquireLock(lexicalLockPath(indexDir, name))
	if err != nil {
		return errSidecarBusy
	}
	defer lock.Release()

	basePath := filepath.Join(indexDir, name, name)
	pm := NewReadOnlyPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return fmt.Errorf("building lexical sidecar: loading passages: %w", err)
	}
	defer pm.Close()
	// Read the stamp before the scan: an edit that lands during the scan then
	// makes the stored stamp stale, and the next load rebuilds again.
	stamp := passagesFileStamp(indexDir, name)

	st, err := store.Open(basePath+".lex.db", store.Options{})
	if err != nil {
		return fmt.Errorf("building lexical sidecar: %w", err)
	}
	defer st.Close()

	analyzer := analysis.New(sidecarLanguage)
	total := 0
	sources := make(map[string]struct{})
	err = st.ReplaceAll(ctx, func(add func(store.SourcedPassage) error) error {
		return pm.ForEachPassage(func(p Passage) error {
			src, _ := p.Metadata["source"].(string)
			sources[src] = struct{}{}
			id := p.ID
			kind := sidecarKind(src)
			total++
			return add(store.SourcedPassage{Source: src, NewPassage: store.NewPassage{
				ID:      &id, // rowid = old passage ID
				VecSlot: &id, // vec_slot = ID (RFC 003 section 6.4 step 2)
				Text:    p.Text,
				Kind:    kind,
				Tokens:  strings.Join(analyzer.Tokens(p.Text, analysis.Kind(kind)), " "),
			}})
		})
	}, nil)
	if err != nil {
		return fmt.Errorf("building lexical sidecar: %w", err)
	}
	// The freshness keys are written last, in their own statements after the
	// data commit; a crash between the two leaves an old stamp, which only
	// causes one more rebuild.
	for _, m := range [][2]string{
		{"analyzer", analysis.ID},
		{"language", analyzer.Language},
		{"passage_count", strconv.Itoa(total)},
		{"source_count", strconv.Itoa(len(sources))},
		{"passages_file", stamp},
	} {
		if err := st.SetMeta(ctx, m[0], m[1]); err != nil {
			return fmt.Errorf("building lexical sidecar: %w", err)
		}
	}
	return nil
}

// sidecarKind selects the analyzer kind from the source file type: prose for
// documents, code for everything else (RFC 003 section 4.1 item 3).
func sidecarKind(source string) string {
	switch strings.ToLower(filepath.Ext(source)) {
	case ".md", ".mdx", ".txt", ".rst", ".adoc", ".org":
		return "prose"
	default:
		return "code"
	}
}

// removeLexicalFiles removes a sidecar database and its WAL companions. Errors
// are ignored: a missing file is the normal case, and a leftover -wal/-shm is
// recreated or cleaned by SQLite on the next open.
func removeLexicalFiles(path string) {
	_ = removeDBFiles(path)
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
