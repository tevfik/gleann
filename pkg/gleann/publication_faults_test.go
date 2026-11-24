package gleann

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every step of finishing a vector publication reports its own failure: the
// generation write, the two pending-key deletes and the transaction itself.
func TestFinishVectorPublicationReportsEachStep(t *testing.T) {
	cases := []struct {
		name string
		ddl  string
		ctx  context.Context
		want string
	}{
		{"begin", "", fxCancelled(), "beginning vector generation write"},
		{"generation", failMeta("vector_generation"), context.Background(), "setting vector generation"},
		{"pending file", fxFail("_del_file", "DELETE", "meta", "OLD.key = 'pending_vector_file'"), context.Background(), "clearing pending vector file"},
		{"pending generation", fxFail("_del_gen", "DELETE", "meta", "OLD.key = 'pending_vector_generation'"), context.Background(), "clearing pending vector generation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, base, _ := fxBuilt(t)
			st := fxOpen(t, base)
			ctx := context.Background()
			if err := st.SetMeta(ctx, "pending_vector_file", "idx.index.tmp"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetMeta(ctx, "pending_vector_generation", "9"); err != nil {
				t.Fatal(err)
			}
			if tc.ddl != "" {
				fxExec(t, base, tc.ddl)
			}
			err := finishVectorPublication(tc.ctx, st, 9)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			// A failed finish leaves the publication pending, not half done.
			if got, _ := st.Meta(ctx, "pending_vector_file"); got != "idx.index.tmp" {
				t.Errorf("pending_vector_file after a failed finish = %q, want it kept", got)
			}
			if got, _ := st.Meta(ctx, "vector_generation"); got == "9" {
				t.Error("vector_generation advanced although the finish failed")
			}
		})
	}
}

// publishVectorFormat2 fails cleanly at each of its steps, and never leaves
// the pending keys behind when the transaction did not commit.
func TestPublishVectorFormat2Failures(t *testing.T) {
	file := func(base string) []stagedVectorFile {
		return []stagedVectorFile{{Suffix: suffixMain, Data: []byte("vec")}}
	}
	noChanges := func(*sql.Tx) error { return nil }

	t.Run("nothing staged", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		err := b.publishVectorFormat2(context.Background(), fxOpen(t, base), base, nil, 2, noChanges)
		if err == nil || !strings.Contains(err.Error(), "nothing staged") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("staging write fails", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		missing := filepath.Join(filepath.Dir(base), "no-such-dir", "idx")
		err := b.publishVectorFormat2(context.Background(), fxOpen(t, base), missing, file(base), 2, noChanges)
		if err == nil || !strings.Contains(err.Error(), "staging vector file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("begin fails", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		err := b.publishVectorFormat2(fxCancelled(), fxOpen(t, base), base, file(base), 2, noChanges)
		if err == nil || !strings.Contains(err.Error(), "beginning vector publication") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("database changes fail", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		boom := errors.New("changes refused")
		err := b.publishVectorFormat2(context.Background(), fxOpen(t, base), base, file(base), 2, func(*sql.Tx) error { return boom })
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the dbChanges error", err)
		}
		if got := f2Meta(t, base, "pending_vector_file"); got != "" {
			t.Errorf("pending_vector_file = %q after a rolled-back publication", got)
		}
	})
	for _, tc := range []struct{ key, want string }{
		{"pending_vector_file", "recording pending vector file"},
		{"pending_vector_generation", "recording pending vector generation"},
		{metaVectorMainGeneration, "recording main vector generation"},
	} {
		t.Run("meta "+tc.key, func(t *testing.T) {
			_, base, b := fxBuilt(t)
			st := fxOpen(t, base)
			fxExec(t, base, failMeta(tc.key))
			err := b.publishVectorFormat2(context.Background(), st, base, file(base), 2, noChanges)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("rename fails and keeps the staging file", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		// A non-empty directory where the vector file belongs makes the rename fail.
		if err := os.Remove(base + ".index"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(base+".index", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := b.publishVectorFormat2(context.Background(), fxOpen(t, base), base, file(base), 2, noChanges)
		if err == nil || !strings.Contains(err.Error(), "publishing vector file") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(base + ".index.tmp"); err != nil {
			t.Errorf("the staging file must survive for recovery: %v", err)
		}
		if got := f2Meta(t, base, "pending_vector_file"); got == "" {
			t.Error("the committed pending key must survive for recovery")
		}
	})
	t.Run("staging file vanished and no vector file", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		if err := os.Remove(base + ".index"); err != nil {
			t.Fatal(err)
		}
		// Something removes the staging file between the commit and the rename.
		b.crashHook = func(point string) error {
			if point == crashAfterCommit {
				return os.Remove(base + ".index.tmp")
			}
			return nil
		}
		err := b.publishVectorFormat2(context.Background(), fxOpen(t, base), base, file(base), 2, noChanges)
		if err == nil || !strings.Contains(err.Error(), "publishing vector file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("another reader already rolled it forward", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		// The staging file is gone but the final file exists: the reader that
		// recovered the publication renamed it. The finish step carries on.
		b.crashHook = func(point string) error {
			if point == crashAfterCommit {
				return os.Remove(base + ".index.tmp")
			}
			return nil
		}
		if err := b.publishVectorFormat2(context.Background(), fxOpen(t, base), base, file(base), 2, noChanges); err != nil {
			t.Fatalf("publication must tolerate a staging file that was already renamed: %v", err)
		}
		if got := f2Meta(t, base, "vector_generation"); got != "2" {
			t.Errorf("vector_generation = %q, want 2", got)
		}
	})
	t.Run("finish fails", func(t *testing.T) {
		_, base, b := fxBuilt(t)
		st := fxOpen(t, base)
		fxExec(t, base, failMeta("vector_generation"))
		err := b.publishVectorFormat2(context.Background(), st, base, file(base), 2, noChanges)
		if err == nil || !strings.Contains(err.Error(), "setting vector generation") {
			t.Fatalf("err = %v", err)
		}
		// The publication is committed and renamed; recovery finishes it.
		if got := f2Meta(t, base, "pending_vector_file"); got == "" {
			t.Error("the pending key must stay so the next open finishes the publication")
		}
	})
}

// Stale staging files that cannot be removed are reported, not ignored.
func TestRemoveStaleVectorStagingReportsFailure(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx")
	if err := os.MkdirAll(filepath.Join(base+".delta.tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := removeStaleVectorStaging(base)
	if err == nil || !strings.Contains(err.Error(), "removing stale vector staging file") {
		t.Fatalf("err = %v", err)
	}
	// Staging files that exist and can be removed are removed.
	if err := os.WriteFile(base+".index.tmp", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(base + ".delta.tmp"); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleVectorStaging(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base + ".index.tmp"); !os.IsNotExist(err) {
		t.Errorf("stale staging file survived: %v", err)
	}
}

// Recovery refuses what it cannot trust and says which step failed.
func TestRecoverVectorPublicationFailures(t *testing.T) {
	setPending := func(t *testing.T, base, files, gen string) {
		t.Helper()
		st := fxOpen(t, base)
		ctx := context.Background()
		if err := st.SetMeta(ctx, "pending_vector_file", files); err != nil {
			t.Fatal(err)
		}
		if gen != "" {
			if err := st.SetMeta(ctx, "pending_vector_generation", gen); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Run("pending file unreadable", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		err := recoverVectorPublication(fxCancelled(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), "reading pending vector file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("stale staging cannot be removed", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		if err := os.MkdirAll(filepath.Join(base+".index.tmp", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := recoverVectorPublication(context.Background(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), "removing stale vector staging file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("pending generation missing", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		setPending(t, base, "idx.index.tmp", "")
		err := recoverVectorPublication(context.Background(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), "gleann index repair idx") {
			t.Fatalf("err = %v, want the repair hint", err)
		}
	})
	t.Run("pending generation garbled", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		setPending(t, base, "idx.index.tmp", "not-a-number")
		err := recoverVectorPublication(context.Background(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), `generation "not-a-number"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("staged name is a path", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		setPending(t, base, filepath.Join("..", "evil.tmp"), "3")
		err := recoverVectorPublication(context.Background(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), "records staged file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("staged file cannot be rolled forward", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		setPending(t, base, "idx.index.tmp", "3")
		if err := os.WriteFile(base+".index.tmp", []byte("new"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(base + ".index"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(base+".index", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := recoverVectorPublication(context.Background(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), "rolling forward vector publication") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("staged file cannot be examined", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		// A NUL byte is an invalid name on every platform: stat fails with
		// something other than "does not exist".
		setPending(t, base, "bad\x00name.tmp", "3")
		err := recoverVectorPublication(context.Background(), fxOpen(t, base), base)
		if err == nil || !strings.Contains(err.Error(), "checking staged vector file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("finish fails", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		st := fxOpen(t, base)
		setPending(t, base, "idx.index.tmp", "3")
		fxExec(t, base, failMeta("vector_generation"))
		err := recoverVectorPublication(context.Background(), st, base)
		if err == nil || !strings.Contains(err.Error(), "setting vector generation") {
			t.Fatalf("err = %v", err)
		}
	})
}

// syncPublishedMeta leaves a missing meta.json alone and reports each
// database read or the meta.json write that fails.
func TestSyncPublishedMetaFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("no meta.json", func(t *testing.T) {
		dir, base, _ := fxBuilt(t)
		if err := os.Remove(filepath.Join(dir, "idx", "idx.meta.json")); err != nil {
			t.Fatal(err)
		}
		if err := syncPublishedMeta(ctx, fxOpen(t, base), base, 5); err != nil {
			t.Fatalf("a half-removed index must be left alone: %v", err)
		}
	})
	t.Run("count fails", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		err := syncPublishedMeta(fxCancelled(), fxOpen(t, base), base, 5)
		if err == nil || !strings.Contains(err.Error(), "counting passages after recovery") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("slot span fails", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		st := fxOpen(t, base)
		fxExec(t, base, `ALTER TABLE passages RENAME COLUMN vec_slot TO vec_slot_x`)
		err := syncPublishedMeta(ctx, st, base, 5)
		if err == nil {
			t.Fatal("a damaged passages table must fail the sync")
		}
	})
	t.Run("vector slots unreadable", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		st := fxOpen(t, base)
		fxExec(t, base, `ALTER TABLE meta RENAME TO meta_x`)
		err := syncPublishedMeta(ctx, st, base, 5)
		if err == nil {
			t.Fatal("an unreadable meta table must fail the sync")
		}
	})
	t.Run("meta.json cannot be written", func(t *testing.T) {
		_, base, _ := fxBuilt(t)
		// An injected fault makes the metadata write fail (the store stays usable).
		blockMetaWrites(t, base+".meta.json")
		err := syncPublishedMeta(ctx, fxOpen(t, base), base, 5)
		if err == nil || !strings.Contains(err.Error(), "syncing meta.json after recovery") || !strings.Contains(err.Error(), "injected metadata write failure") {
			t.Fatalf("err = %v", err)
		}
	})
}
