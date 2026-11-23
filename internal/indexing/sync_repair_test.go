package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// An index whose last source is deleted must not be emptied when the vector
// file cannot be removed: the store, meta.json and the vector file must stay
// in sync, the failure must be returned as transient (no rebuild), and the
// next sync must retry instead of reporting "already up to date".
func TestSyncEmptyUpdateVectorRemovalFailureKeepsIndexConsistent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "notes.txt"), "The only note about the quagga colony.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "empty", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "empty")
	if err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "empty")
	if len(before) == 0 {
		t.Fatal("fixture index has no passages")
	}

	// A non-empty directory at <name>.index makes os.Remove fail with
	// ENOTEMPTY, the same error class as an open handle on Windows.
	base := filepath.Join(env.cfg.IndexDir, "empty", "empty")
	if err := os.Remove(base + ".index"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base+".index", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(docs, "notes.txt")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "empty"})
	if err == nil || !strings.Contains(err.Error(), "remove vector file") {
		t.Fatalf("Sync = %+v, %v; want the vector-file removal error", res, err)
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil: the index was not updated", res)
	}
	if strings.Contains(out.String(), "falling back to full rebuild") {
		t.Errorf("a transient removal failure must not start a full rebuild:\n%s", out.String())
	}

	// SQLite and meta.json still describe the same, intact index.
	if after := passageSet(t, env.cfg, "empty"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("passages changed after a failed empty update:\nbefore=%v\nafter=%v", before, after)
	}
	afterMeta, err := gleann.GetIndexMeta(env.cfg.IndexDir, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if afterMeta.NumPassages != built.NumPassages || afterMeta.VectorReady != built.VectorReady {
		t.Errorf("meta.json changed after a failed empty update: %+v -> %+v", built, afterMeta)
	}
	// The index was already damaged by the directory planted at <name>.index,
	// and the health check says so: a directory is not a readable vector file.
	// What this test cares about is that the failed update added nothing on top
	// of that pre-existing damage - exactly one problem, and it is the one the
	// fixture created. (Before the health check learned to open the main vector
	// file, it called this index healthy and hid the damage.)
	if h, err := gleann.CheckIndexHealth(env.cfg.IndexDir, "empty"); err != nil ||
		len(h.Problems) != 1 || !strings.Contains(strings.Join(h.Problems, "\n"), "the vector index file cannot be read") {
		t.Errorf("the health report after a failed empty update should be only the planted directory: %+v (%v)", h, err)
	}
	if h, _ := gleann.CheckIndexHealth(env.cfg.IndexDir, "empty"); h != nil && h.Passages != h.MetaPassages {
		t.Errorf("store/meta passage counts diverged: %d vs %d", h.Passages, h.MetaPassages)
	}
	if HasSyncBackup(env.cfg.IndexDir, "empty") {
		t.Error("the rollback copy was left behind")
	}

	// The failure is retryable: the next sync tries the same empty update and
	// fails again instead of claiming the index is up to date.
	res, err = Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "empty"})
	if err == nil || res != nil {
		t.Fatalf("retry Sync = %+v, %v; want the same transient error, not up to date", res, err)
	}
}

// A deleted <name>.db on a format-2 index that had a code graph must trigger
// the rollback-protected full rebuild (and restore the graph), not a silent
// in-place update that loses it.
func TestSyncMissingStoreRebuildsGraph(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	if !gleann.HasCodeGraph(env.cfg.IndexDir, "g") {
		t.Fatal("fixture index has no code graph")
	}

	base := filepath.Join(env.cfg.IndexDir, "g", "g")
	for _, suffix := range []string{".db", ".db-wal", ".db-shm"} {
		if err := os.Remove(base + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(docs, "a.go"), "package fx\n\n// Helper does the work.\nfunc Helper(x int) int { return x + 2 }\n")

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g"})
	if err != nil {
		t.Fatalf("Sync after deleting the store: %v\n%s", err, out.String())
	}
	if !res.FullRebuild {
		t.Errorf("res = %+v, want a full rebuild: the record store and its graph were lost", res)
	}
	if !strings.Contains(out.String(), "database was recreated") {
		t.Errorf("no recreated-database notice:\n%s", out.String())
	}
	if !gleann.HasCodeGraph(env.cfg.IndexDir, "g") {
		t.Fatal("the code graph was lost by the sync")
	}
	meta, err := gleann.GetIndexMeta(env.cfg.IndexDir, "g")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.HasGraph {
		t.Error("meta.json does not record has_graph after the graph was restored")
	}
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols`); n < 2 {
		t.Errorf("symbols rows after the rebuild = %d, want the graph rebuilt (>= 2)", n)
	}
	if h, err := gleann.CheckIndexHealth(env.cfg.IndexDir, "g"); err != nil || h.NeedsRepair() {
		t.Errorf("the rebuilt index is unhealthy: %+v (%v)", h, err)
	}
}

// A changed source that exists but cannot be read must fail the incremental
// sync before anything is mutated: its old passages must not be deleted and
// the sync must not report success.
func TestSyncUnreadableChangedSourceFailsWithoutDeletingPassages(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	t.Setenv("HOME", t.TempDir())
	env := newEnv(t)
	docs := t.TempDir()
	notes := filepath.Join(docs, "notes.txt")
	writeFile(t, notes, "First notes about the quokka colony.\n")
	writeFile(t, filepath.Join(docs, "other.txt"), "Stable notes about the wombats.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "ro", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "ro")
	if err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "ro")

	writeFile(t, notes, "Rewritten notes about the quokka colony.\n")
	if err := os.Chmod(notes, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(notes, 0o644) })

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{
		Options: env.options(&out),
		Name:    "ro",
		Files:   []string{notes},
	})
	if err == nil {
		t.Fatalf("Sync succeeded over an unreadable changed source and lost its passages:\n%s", out.String())
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil: the index must not be updated", res)
	}
	if strings.Contains(out.String(), "falling back to full rebuild") {
		t.Errorf("an unreadable source must not start a full rebuild:\n%s", out.String())
	}
	if after := passageSet(t, env.cfg, "ro"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("passages changed although the source could not be read:\nbefore=%v\nafter=%v", before, after)
	}
	assertUntouched(t, env, "ro", *built)
	if HasSyncBackup(env.cfg.IndexDir, "ro") {
		t.Error("the rollback copy was left behind")
	}
}

// A rebuild fallback over an empty corpus cannot produce an index. It must fail
// so withIndexRollback restores the previous index instead of deleting the only
// copy and reporting a successful sync.
func TestSyncEmptyCorpusRebuildRestoresTheIndex(t *testing.T) {
	// The three ways into the fallback after the corpus became empty.
	t.Run("delta vector file cannot be removed", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		env := newEnv(t)
		docs := t.TempDir()
		notes := filepath.Join(docs, "notes.txt")
		writeFile(t, notes, "The only note about the quagga colony.\n")
		if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "em", DocsDir: docs}); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(env.cfg.IndexDir, "em", "em")
		_ = os.Remove(base + ".delta")
		// A non-empty directory at <name>.delta: os.Remove fails with
		// ENOTEMPTY after .index was already removed.
		if err := os.MkdirAll(filepath.Join(base+".delta", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		before := passageSet(t, env.cfg, "em")
		if err := os.Remove(notes); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "em"})
		if err == nil {
			t.Fatalf("Sync reported success and destroyed the index:\n%s", out.String())
		}
		if res != nil {
			t.Fatalf("res = %+v, want nil: no index was written", res)
		}
		if !errors.Is(err, ErrNoDocuments) {
			t.Errorf("err = %v, want the empty-corpus rebuild failure (%v)", err, ErrNoDocuments)
		}
		if _, statErr := os.Stat(filepath.Join(env.cfg.IndexDir, "em", "em.meta.json")); statErr != nil {
			t.Fatalf("the index was destroyed: %v", statErr)
		}
		if after := passageSet(t, env.cfg, "em"); strings.Join(after, "\n") != strings.Join(before, "\n") {
			t.Errorf("passages changed:\nbefore=%v\nafter=%v", before, after)
		}
		if HasSyncBackup(env.cfg.IndexDir, "em") {
			t.Error("the rollback copy was left behind")
		}
	})

	t.Run("index metadata cannot be written", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		env := newEnv(t)
		docs := t.TempDir()
		notes := filepath.Join(docs, "notes.txt")
		writeFile(t, notes, "The only note about the margay colony.\n")
		if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "mw", DocsDir: docs}); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(env.cfg.IndexDir, "mw", "mw")
		// A directory at <name>.meta.json.tmp makes the atomic metadata write
		// fail with EISDIR after the store has already been emptied.
		if err := os.MkdirAll(filepath.Join(base+".meta.json.tmp", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(notes); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "mw"})
		if err == nil {
			t.Fatalf("Sync reported success and destroyed the index:\n%s", out.String())
		}
		if res != nil {
			t.Fatalf("res = %+v, want nil: no index was written", res)
		}
		if !errors.Is(err, ErrNoDocuments) {
			t.Errorf("err = %v, want the empty-corpus rebuild failure (%v)", err, ErrNoDocuments)
		}
		if _, statErr := os.Stat(filepath.Join(env.cfg.IndexDir, "mw", "mw.meta.json")); statErr != nil {
			t.Fatalf("the index was destroyed: %v", statErr)
		}
		if HasSyncBackup(env.cfg.IndexDir, "mw") {
			t.Error("the rollback copy was left behind")
		}
	})

	t.Run("store missing and every source deleted", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		env := newEnv(t)
		docs := t.TempDir()
		notes := filepath.Join(docs, "notes.txt")
		writeFile(t, notes, "The only note about the ocelot colony.\n")
		if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "sd", DocsDir: docs}); err != nil {
			t.Fatal(err)
		}
		built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "sd")
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(env.cfg.IndexDir, "sd", "sd")
		for _, suffix := range []string{".db", ".db-wal", ".db-shm"} {
			if err := os.Remove(base + suffix); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.Remove(notes); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "sd"})
		if err == nil {
			t.Fatalf("Sync reported success and destroyed the index:\n%s", out.String())
		}
		if res != nil {
			t.Fatalf("res = %+v, want nil: no index was written", res)
		}
		if !errors.Is(err, ErrNoDocuments) {
			t.Errorf("err = %v, want the empty-corpus rebuild failure (%v)", err, ErrNoDocuments)
		}
		after, err := gleann.GetIndexMeta(env.cfg.IndexDir, "sd")
		if err != nil {
			t.Fatalf("the index was destroyed: %v", err)
		}
		if after.NumPassages != built.NumPassages || after.EmbeddingModel != built.EmbeddingModel {
			t.Errorf("meta.json changed: %+v -> %+v", built, after)
		}
		if HasSyncBackup(env.cfg.IndexDir, "sd") {
			t.Error("the rollback copy was left behind")
		}
	})
}

// stripHasGraph rewrites meta.json without has_graph, the way an index built
// before the field existed looks.
func stripHasGraph(t *testing.T, indexDir, name string) {
	t.Helper()
	path := filepath.Join(indexDir, name, name+".meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["has_graph"]; !ok {
		t.Fatalf("fixture meta.json has no has_graph: %s", data)
	}
	delete(raw, "has_graph")
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// An index built before meta.json recorded has_graph must be upgraded by the
// next sync (even a no-op one): otherwise losing <name>.db silently drops the
// code graph, because the store loss is indistinguishable from an index that
// never had a graph.
func TestSyncBackfillsLegacyHasGraph(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	stripHasGraph(t, env.cfg.IndexDir, "g")

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g"})
	if err != nil {
		t.Fatalf("up-to-date Sync: %v\n%s", err, out.String())
	}
	if !res.UpToDate {
		t.Fatalf("first sync = %+v, want up to date", res)
	}
	meta, err := gleann.GetIndexMeta(env.cfg.IndexDir, "g")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.HasGraph {
		t.Errorf("an up-to-date sync did not backfill has_graph for a legacy index:\n%s", out.String())
	}

	// The store is lost later: the backfilled meta.json must make the sync
	// rebuild the graph without --graph.
	base := filepath.Join(env.cfg.IndexDir, "g", "g")
	for _, suffix := range []string{".db", ".db-wal", ".db-shm"} {
		if err := os.Remove(base + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	out.Reset()
	res, err = Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g"})
	if err != nil {
		t.Fatalf("Sync after deleting the store: %v\n%s", err, out.String())
	}
	if !res.FullRebuild {
		t.Errorf("res = %+v, want a full rebuild: the record store and its graph were lost", res)
	}
	if !gleann.HasCodeGraph(env.cfg.IndexDir, "g") {
		t.Fatalf("a legacy index lost its code graph when the store was deleted:\n%s", out.String())
	}
	meta, err = gleann.GetIndexMeta(env.cfg.IndexDir, "g")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.HasGraph {
		t.Error("meta.json does not record has_graph after the graph was restored")
	}
}

// newMediaEnv is a fake Ollama that embeds deterministically, advertises a
// vision model and describes images. Setting the returned flag makes /api/chat
// fail, the usual transient failure of media indexing.
func newMediaEnv(t *testing.T) (*testEnv, *atomic.Bool) {
	t.Helper()
	var chatFails atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]any{"families": []string{"clip"}}})
		case "/api/chat":
			if chatFails.Load() {
				http.Error(w, "vision model crashed", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "A photograph of a quokka in the enclosure."}})
		default: // /api/embed
			var req struct {
				Input any `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var texts []string
			switch v := req.Input.(type) {
			case []any:
				for _, s := range v {
					str, _ := s.(string)
					texts = append(texts, str)
				}
			case string:
				texts = []string{v}
			}
			embs := make([][]float32, len(texts))
			for i, txt := range texts {
				e := make([]float32, 8)
				for j := range e {
					e[j] = float32((len(txt)+j*7)%13) / 13
				}
				e[i%8]++
				embs[i] = e
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())

	env := newEnv(t)
	env.cfg.OllamaHost = srv.URL
	return env, &chatFails
}

// A changed media file whose multimodal extraction fails must fail the sync
// before its old passages are removed: the vision model being down is the most
// common transient failure and must not silently unindex the file.
func TestSyncMultimodalFailureKeepsPassages(t *testing.T) {
	env, chatFails := newMediaEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "notes.txt"), "Stable notes about the wombats.\n")
	photo := filepath.Join(docs, "photo.png")
	writeFile(t, photo, "PNG-fake-quokka-v1")

	opts := env.options(nil)
	opts.MultimodalModel = "fake-vision"
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "mm", DocsDir: docs}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "mm")
	if err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "mm")
	if !strings.Contains(strings.Join(before, "\n"), "photo.png|A photograph of a quokka") {
		t.Fatalf("fixture index has no multimodal passage: %v", before)
	}

	// Changed content: a new cache fingerprint, so the description is really
	// requested again.
	writeFile(t, photo, "PNG-fake-quokka-v2")
	chatFails.Store(true)

	var out bytes.Buffer
	syncOpts := env.options(&out)
	syncOpts.MultimodalModel = "fake-vision"
	res, err := Sync(context.Background(), SyncOptions{Options: syncOpts, Name: "mm", Files: []string{photo}})
	if err == nil {
		t.Fatalf("Sync succeeded over a failing multimodal extraction and deleted photo.png:\n%s", out.String())
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil: the index must not be updated", res)
	}
	if strings.Contains(out.String(), "falling back to full rebuild") {
		t.Errorf("a failing multimodal extraction must not start a full rebuild:\n%s", out.String())
	}
	if after := passageSet(t, env.cfg, "mm"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("passages changed although the image could not be described:\nbefore=%v\nafter=%v", before, after)
	}
	assertUntouched(t, env, "mm", *built)
	if HasSyncBackup(env.cfg.IndexDir, "mm") {
		t.Error("the rollback copy was left behind")
	}
}

// An os.Stat error that is not fs.ErrNotExist (a transiently unreadable parent
// directory) is not a deletion: the sync must fail before removing passages.
func TestSyncChangedStatErrorIsNotADeletion(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	t.Setenv("HOME", t.TempDir())
	env := newEnv(t)
	docs := t.TempDir()
	sub := filepath.Join(docs, "sub")
	notes := filepath.Join(sub, "notes.txt")
	writeFile(t, notes, "Notes about the quokka colony in the subdirectory.\n")
	writeFile(t, filepath.Join(docs, "other.txt"), "Stable notes about the wombats.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "pd", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "pd")
	if err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "pd")

	writeFile(t, notes, "Rewritten notes about the quokka colony.\n")
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
	defer func() { _ = os.Chmod(sub, 0o755) }()

	var out bytes.Buffer
	res, err := SyncChanged(context.Background(), SyncOptions{Options: env.options(&out), Name: "pd"}, []string{notes})
	if err == nil {
		t.Fatalf("SyncChanged treated an unreadable parent directory as a deletion:\n%s", out.String())
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil: the index must not be updated", res)
	}
	if after := passageSet(t, env.cfg, "pd"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("passages changed on a stat error:\nbefore=%v\nafter=%v", before, after)
	}
	assertUntouched(t, env, "pd", *built)
	if HasSyncBackup(env.cfg.IndexDir, "pd") {
		t.Error("the rollback copy was left behind")
	}

	// A file that really is gone is still a deletion.
	if err := os.Chmod(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(notes); err != nil {
		t.Fatal(err)
	}
	var out2 bytes.Buffer
	res, err = SyncChanged(context.Background(), SyncOptions{Options: env.options(&out2), Name: "pd"}, []string{notes})
	if err != nil {
		t.Fatalf("SyncChanged over a deleted file: %v\n%s", err, out2.String())
	}
	if len(res.Deleted) != 1 {
		t.Errorf("res.Deleted = %v, want the vanished file", res.Deleted)
	}
	if after := passageSet(t, env.cfg, "pd"); strings.Contains(strings.Join(after, "\n"), "sub/notes.txt") {
		t.Errorf("the deleted file's passages were kept: %v", after)
	}
}

// An index whose main vector file is already gone is in a partial state: a
// failing second removal must be classified as requiring a rebuild, not as a
// transient error that leaves the index broken on every retry.
func TestSyncMissingMainVectorFileNeedsARebuild(t *testing.T) {
	t.Run("a remaining source rebuilds cleanly", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		env := newEnv(t)
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "kept.txt"), "Notes about the lynx colony.\n")
		writeFile(t, filepath.Join(docs, "gone.txt"), "Notes about the serval colony.\n")
		if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "st", DocsDir: docs}); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(env.cfg.IndexDir, "st", "st")
		if err := os.Remove(base + ".index"); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(base + ".delta")
		if err := os.MkdirAll(filepath.Join(base+".delta", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(docs, "gone.txt")); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "st"})
		if err != nil {
			t.Fatalf("Sync = %+v, %v; want the index repaired:\n%s", res, err, out.String())
		}
		if _, err := os.Stat(base + ".index"); err != nil {
			t.Errorf("the main vector file was not recreated: %v\n%s", err, out.String())
		}
		h, err := gleann.CheckIndexHealth(env.cfg.IndexDir, "st")
		if err != nil {
			t.Fatal(err)
		}
		for _, prob := range h.Problems {
			if strings.Contains(prob, "vector index file is missing") {
				t.Errorf("the repaired index still misses its vector file: %q", prob)
			}
		}
		passages := passageSet(t, env.cfg, "st")
		if got := strings.Join(passages, "\n"); strings.Contains(got, "gone.txt") || !strings.Contains(got, "kept.txt") {
			t.Errorf("passages after the repair = %v", passages)
		}
	})

	t.Run("an empty corpus restores the previous index", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		env := newEnv(t)
		docs := t.TempDir()
		notes := filepath.Join(docs, "notes.txt")
		writeFile(t, notes, "The only note about the caracal colony.\n")
		if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "et", DocsDir: docs}); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(env.cfg.IndexDir, "et", "et")
		if err := os.Remove(base + ".index"); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(base + ".delta")
		if err := os.MkdirAll(filepath.Join(base+".delta", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(notes); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "et"})
		if err == nil {
			t.Fatalf("Sync succeeded on a broken index:\n%s", out.String())
		}
		if res != nil {
			t.Fatalf("res = %+v, want nil", res)
		}
		if !errors.Is(err, ErrNoDocuments) || !strings.Contains(err.Error(), "previous index restored") {
			t.Fatalf("err = %v, want a rebuild-classified failure with the previous index restored:\n%s", err, out.String())
		}
		if _, statErr := os.Stat(filepath.Join(env.cfg.IndexDir, "et", "et.meta.json")); statErr != nil {
			t.Fatalf("the index was destroyed: %v", statErr)
		}
		if HasSyncBackup(env.cfg.IndexDir, "et") {
			t.Error("the rollback copy was left behind")
		}
	})
}

// The store-loss repair must not silently drop an unreadable source: the full
// read is strict on that path, so the sync fails with the previous index
// restored and the file is indexed once it is readable again.
func TestSyncMissingStoreUnreadableSourceFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	t.Setenv("HOME", t.TempDir())
	env := newEnv(t)
	docs := t.TempDir()
	notes := filepath.Join(docs, "notes.txt")
	writeFile(t, notes, "First notes about the quokka colony.\n")
	writeFile(t, filepath.Join(docs, "other.txt"), "Stable notes about the wombats.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "dr", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "dr")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(env.cfg.IndexDir, "dr", "dr")
	beforeVector, err := os.ReadFile(base + ".index")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".db", ".db-wal", ".db-shm"} {
		if err := os.Remove(base + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(notes, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(notes, 0o644) })
	defer func() { _ = os.Chmod(notes, 0o644) }()

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "dr"})
	if err == nil {
		t.Fatalf("the store-loss repair dropped an unreadable source and reported success:\n%s", out.String())
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil: no index was written", res)
	}
	afterMeta, err := gleann.GetIndexMeta(env.cfg.IndexDir, "dr")
	if err != nil {
		t.Fatalf("the previous index was not restored: %v", err)
	}
	if afterMeta.NumPassages != built.NumPassages || afterMeta.EmbeddingModel != built.EmbeddingModel {
		t.Errorf("meta.json changed: %+v -> %+v", built, afterMeta)
	}
	afterVector, err := os.ReadFile(base + ".index")
	if err != nil {
		t.Fatalf("the vector file was lost: %v", err)
	}
	if string(afterVector) != string(beforeVector) {
		t.Error("the vector file changed although the read failed")
	}
	if HasSyncBackup(env.cfg.IndexDir, "dr") {
		t.Error("the rollback copy was left behind")
	}

	// Readable again: the repair completes with both sources.
	if err := os.Chmod(notes, 0o644); err != nil {
		t.Fatal(err)
	}
	var out2 bytes.Buffer
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(&out2), Name: "dr"}); err != nil {
		t.Fatalf("Sync after fixing the permissions: %v\n%s", err, out2.String())
	}
	passages := strings.Join(passageSet(t, env.cfg, "dr"), "\n")
	if !strings.Contains(passages, "notes.txt") || !strings.Contains(passages, "other.txt") {
		t.Errorf("passages after the repair = %q", passages)
	}
}

// The recreated-database notice must not claim a code graph rebuild on an index
// that never had a graph.
func TestSyncRecreatedNoticeWithoutAGraph(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "notes.txt"), "Notes about the pangolin colony.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "ng", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	if gleann.HasCodeGraph(env.cfg.IndexDir, "ng") {
		t.Fatal("fixture index has a code graph")
	}
	base := filepath.Join(env.cfg.IndexDir, "ng", "ng")
	for _, suffix := range []string{".db", ".db-wal", ".db-shm"} {
		if err := os.Remove(base + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "ng"})
	if err != nil {
		t.Fatalf("Sync: %v\n%s", err, out.String())
	}
	if !res.FullRebuild {
		t.Errorf("res = %+v, want a full rebuild", res)
	}
	if strings.Contains(out.String(), "code graph is rebuilt") {
		t.Errorf("the notice claims a graph rebuild on an index without a graph:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "database was recreated") {
		t.Errorf("no recreated-database notice:\n%s", out.String())
	}
	if gleann.HasCodeGraph(env.cfg.IndexDir, "ng") {
		t.Error("a code graph was created for an index that never had one")
	}
	if m, err := gleann.GetIndexMeta(env.cfg.IndexDir, "ng"); err != nil || m.HasGraph {
		t.Errorf("meta.HasGraph = %v (%v), want false", m, err)
	}
}

// A directory where the main vector file must be passes a bare existence
// check: os.Stat succeeds on a directory and so does os.Open. The incremental
// path used to accept it and append to the delta, reporting an in-place update
// that left an index whose vector file the searcher cannot open. Such an index
// has to be routed to the rollback-protected full rebuild, which publishes a
// real vector file by rename.
func TestSyncMainVectorFileReplacedByDirectoryRebuilds(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "a.txt"), "The quagga colony gathers at the river bend.\n")
	writeFile(t, filepath.Join(docs, "b.txt"), "Colony notes from the first season of observations.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "dv", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(env.cfg.IndexDir, "dv", "dv")
	if err := os.Remove(base + ".index"); err != nil {
		t.Fatal(err)
	}
	// Non-empty: a rename onto it fails with ENOTEMPTY, which is how an
	// interrupted or hijacked publication leaves the slot occupied.
	if err := os.MkdirAll(filepath.Join(base+".index", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A changed source keeps passages live, so the update has work to do.
	writeFile(t, filepath.Join(docs, "b.txt"), "Colony notes from the second season, with weight counts.\n")

	var out bytes.Buffer
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "dv"})
	if err != nil {
		t.Fatalf("Sync: %v\n%s", err, out.String())
	}
	if res == nil {
		t.Fatalf("res = nil\n%s", out.String())
	}
	// The point of the repair: a real, readable vector file must be left
	// behind, not a directory a bare existence check waved through.
	info, statErr := os.Stat(base + ".index")
	if statErr != nil {
		t.Fatalf("main vector file after the sync: %v", statErr)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("the main vector file is mode %s, want a regular file: dense search cannot read it\n%s", info.Mode(), out.String())
	}
	if !res.FullRebuild {
		t.Errorf("res = %+v, want a full rebuild: the main vector file was not a file", res)
	}
	if h, hErr := gleann.CheckIndexHealth(env.cfg.IndexDir, "dv"); hErr != nil || h.NeedsRepair() {
		t.Errorf("the index is unhealthy after the sync: %+v (%v)", h, hErr)
	}
	s := gleann.NewSearcher(env.cfg, nil)
	if err := s.Load(context.Background(), "dv"); err != nil {
		t.Fatalf("searcher Load after the sync: %v", err)
	}
	defer s.Close()
	if after := passageSet(t, env.cfg, "dv"); len(after) == 0 {
		t.Error("the synced index holds no passages")
	}
	if HasSyncBackup(env.cfg.IndexDir, "dv") {
		t.Error("the rollback copy was left behind")
	}
}
