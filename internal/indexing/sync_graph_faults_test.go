package indexing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// A graph whose dirty files cannot be listed must not be reported as up to
// date: the sync fails with ErrGraphUpdate so the next one retries.
func TestSyncUpToDateReportsUnreadableGraph(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	execDB(t, filepath.Join(env.cfg.IndexDir, "g", "g.db"), `ALTER TABLE files RENAME COLUMN graph_dirty TO zz`)

	_, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Graph: true})
	if !errors.Is(err, ErrGraphUpdate) {
		t.Fatalf("err = %v, want ErrGraphUpdate", err)
	}
	mustContain(t, err, "listing dirty graph files")
}

// A graph write that fails after the vector index was updated is reported
// with ErrGraphUpdate; the manifest does not advance, so the next sync
// re-reads the file.
func TestSyncGraphWriteFailure(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	execDB(t, filepath.Join(env.cfg.IndexDir, "g", "g.db"), abortOn("INSERT", "symbols"))

	writeFile(t, filepath.Join(docs, "a.go"), "package fx\n\nfunc Helper(x int) int { return x + 7 }\n")
	_, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Graph: true})
	if !errors.Is(err, ErrGraphUpdate) {
		t.Fatalf("err = %v, want ErrGraphUpdate", err)
	}
}

// A lexical-only index is synced without embeddings; a failing lexical write
// is reported as such.
func TestSyncLexicalOnlyWriteFailure(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "lx", DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatal(err)
	}
	meta, err := gleann.GetIndexMeta(env.cfg.IndexDir, "lx")
	if err != nil || !meta.LexicalOnly {
		t.Fatalf("precondition: lexical-only index, got %+v, %v", meta, err)
	}
	writeFile(t, filepath.Join(docs, "extra.txt"), "An extra note about narwhals.")
	execDB(t, filepath.Join(env.cfg.IndexDir, "lx", "lx.db"), abortOn("INSERT", "passages"))
	_, err = Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "lx"})
	mustContain(t, err, "updating lexical index")
}
