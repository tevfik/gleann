package indexing

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// recordComputer is a flakyComputer that records every batch of texts it is
// asked to embed, so a test can tell "only the changed file was embedded"
// from "the whole corpus was re-embedded".
type recordComputer struct {
	*flakyComputer

	mu    sync.Mutex
	calls [][]string
}

func (r *recordComputer) record(texts []string) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), texts...))
	r.mu.Unlock()
}

func (r *recordComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	r.record(texts)
	return r.flakyComputer.Compute(ctx, texts)
}

func (r *recordComputer) ComputeSingle(ctx context.Context, s string) ([]float32, error) {
	r.record([]string{s})
	return r.flakyComputer.ComputeSingle(ctx, s)
}

// recorded returns the batches sent to the embedder so far.
func (r *recordComputer) recorded() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// A transient embedder failure during an incremental sync is returned as it
// is: the index is not rebuilt (a rebuild would fail the same way) and the
// existing index is not touched.
func TestSyncEmbedderDownReturnsWithoutRebuild(t *testing.T) {
	env, fc, opts := flakyEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "down", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "down")
	if err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "down")

	// One changed file; the embedder is down for the whole sync.
	writeFile(t, filepath.Join(docs, "notes.txt"), "Fresh notes about the quokka colony.\n")
	fc.down.Store(true)
	rc := &recordComputer{flakyComputer: fc}
	var out bytes.Buffer
	o := opts
	o.Embedder = rc
	w := &lockedBuffer{b: &out}
	o.Stdout, o.Stderr = w, w

	res, err := Sync(context.Background(), SyncOptions{Options: o, Name: "down"})
	if err == nil || !errors.Is(err, errEmbedderDown) {
		t.Fatalf("err = %v, want the embedder error", err)
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil: a failed sync reports no result", res)
	}
	if strings.Contains(out.String(), "falling back to full rebuild") {
		t.Errorf("an embedder failure must not start a full rebuild:\n%s", out.String())
	}
	if strings.Contains(out.String(), "previous index restored") {
		t.Errorf("no rebuild means nothing had to be rolled back:\n%s", out.String())
	}
	assertUntouched(t, env, "down", *built)
	if after := passageSet(t, env.cfg, "down"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("the index passages changed:\n%v\n%v", before, after)
	}
	if HasSyncBackup(env.cfg.IndexDir, "down") {
		t.Error("the rollback copy was left behind")
	}

	// The only embedding work is the changed file: no untouched passage was
	// sent to the embedder again.
	calls := rc.recorded()
	if len(calls) != 1 {
		t.Fatalf("embedder called %d times, want 1 (the changed file only)", len(calls))
	}
	if len(calls[0]) == 0 || len(calls[0]) > 4 {
		t.Fatalf("embedded %d texts, want the changed file's chunks", len(calls[0]))
	}
	for _, text := range calls[0] {
		if !strings.Contains(text, "quokka") {
			t.Errorf("an untouched passage was embedded: %q", text)
		}
	}
}

// SyncChanged (the watch batch) shares Sync's classification: an embedder
// failure is returned without a full rebuild.
func TestSyncChangedEmbedderDownReturnsWithoutRebuild(t *testing.T) {
	env, fc, opts := flakyEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "downb", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "downb")
	if err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "downb")

	changed := filepath.Join(docs, "notes.txt")
	writeFile(t, changed, "More notes about the quokka colony.\n")
	fc.down.Store(true)
	rc := &recordComputer{flakyComputer: fc}
	var out bytes.Buffer
	o := opts
	o.Embedder = rc
	w := &lockedBuffer{b: &out}
	o.Stdout, o.Stderr = w, w

	res, err := SyncChanged(context.Background(), SyncOptions{Options: o, Name: "downb"}, []string{changed})
	if err == nil || !errors.Is(err, errEmbedderDown) {
		t.Fatalf("err = %v, want the embedder error", err)
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil", res)
	}
	if strings.Contains(out.String(), "falling back to full rebuild") {
		t.Errorf("SyncChanged must classify exactly like Sync:\n%s", out.String())
	}
	assertUntouched(t, env, "downb", *built)
	if after := passageSet(t, env.cfg, "downb"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("the index passages changed:\n%v\n%v", before, after)
	}
	if calls := rc.recorded(); len(calls) != 1 {
		t.Errorf("embedder called %d times, want 1", len(calls))
	}
}

// A store the incremental update cannot read is still repaired by the
// full-rebuild fallback: it is the one class a rebuild from the sources can
// fix, and the rollback keeps the old index if even that fails.
func TestSyncDamagedStoreStillFallsBackToFullRebuild(t *testing.T) {
	corrupt := func(t *testing.T, base string) {
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(base + ".db" + suffix)
		}
		if err := os.WriteFile(base+".db", []byte(strings.Repeat("not a database ", 200)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		run  func(t *testing.T, o Options, docs string) (*SyncResult, error)
	}{
		{"sync", func(t *testing.T, o Options, docs string) (*SyncResult, error) {
			return Sync(context.Background(), SyncOptions{Options: o, Name: "dmg"})
		}},
		{"sync changed", func(t *testing.T, o Options, docs string) (*SyncResult, error) {
			return SyncChanged(context.Background(), SyncOptions{Options: o, Name: "dmg"}, []string{filepath.Join(docs, "notes.txt")})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			env := newEnv(t)
			docs := fixture(t)
			if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "dmg", DocsDir: docs}); err != nil {
				t.Fatal(err)
			}
			corrupt(t, filepath.Join(env.cfg.IndexDir, "dmg", "dmg"))
			writeFile(t, filepath.Join(docs, "notes.txt"), "Rewritten notes about wombats.\n")

			var out bytes.Buffer
			o := env.options(&out)
			res, err := tc.run(t, o, docs)
			if err != nil {
				t.Fatalf("sync: %v\n%s", err, out.String())
			}
			if !res.FullRebuild {
				t.Fatalf("res = %+v, want a full rebuild: a damaged store can only be rebuilt", res)
			}
			if res.Update != nil {
				t.Errorf("a full rebuild reports no incremental update: %+v", res.Update)
			}
			if !strings.Contains(out.String(), "falling back to full rebuild") {
				t.Errorf("fallback not announced:\n%s", out.String())
			}
			if got := passageSet(t, env.cfg, "dmg"); !strings.Contains(strings.Join(got, "\n"), "wombats") {
				t.Errorf("the rebuilt index lacks the changed file: %v", got)
			}
			if h, err := gleann.CheckIndexHealth(env.cfg.IndexDir, "dmg"); err != nil || h.NeedsRepair() {
				t.Errorf("the rebuilt index is unhealthy: %+v (%v)", h, err)
			}
			if HasSyncBackup(env.cfg.IndexDir, "dmg") {
				t.Error("the rollback copy was left behind")
			}
		})
	}
}
