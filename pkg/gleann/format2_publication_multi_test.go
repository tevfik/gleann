package gleann

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A publication that stages two files (RFC 005 section 4): at every crash
// point recovery leaves the old pair (crash before the commit) or the new
// pair (any later point), never a lost pending key or a leftover staging
// file, and the pending list is cleared.
func TestFormat2_TwoFilePublicationCrashPoints(t *testing.T) {
	points := []string{crashBeforeCommit, crashAfterCommit, crashAfterMergeCommit, crashAfterRenameMain, crashAfterRename, crashAfterFinish}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			b, _ := newFormat2Builder(t, dir, 8)
			ctx := context.Background()
			if err := b.Build(ctx, "idx", f2Items()); err != nil {
				t.Fatalf("Build: %v", err)
			}
			base := filepath.Join(dir, "idx", "idx")
			oldMain, err := os.ReadFile(base + ".index")
			if err != nil {
				t.Fatal(err)
			}
			newMain := append(bytes.Clone(oldMain), 0)
			newDelta := []byte("new delta")
			if err := os.WriteFile(base+".delta", []byte("old delta"), 0o644); err != nil {
				t.Fatal(err)
			}

			st, err := openFormat2Store(base)
			if err != nil {
				t.Fatal(err)
			}
			b.crashHook = crashAt(point)
			err = b.publishVectorFormat2(ctx, st, base, []stagedVectorFile{
				{Suffix: suffixDelta, Data: newDelta},
				{Suffix: suffixMain, Data: newMain},
			}, 2, func(*sql.Tx) error { return nil })
			if err == nil {
				t.Fatal("the publication must fail at the injected crash")
			}
			st.Close()

			// Recovery runs on the next open.
			st2, err := openFormat2Store(base)
			if err != nil {
				t.Fatal(err)
			}
			defer st2.Close()
			if err := recoverVectorPublication(ctx, st2, base); err != nil {
				t.Fatalf("recoverVectorPublication: %v", err)
			}

			gotMain, _ := os.ReadFile(base + ".index")
			gotDelta, _ := os.ReadFile(base + ".delta")
			if point == crashBeforeCommit {
				if !bytes.Equal(gotMain, oldMain) || string(gotDelta) != "old delta" {
					t.Errorf("a crash before the commit must keep the old pair (main %d bytes, delta %q)", len(gotMain), gotDelta)
				}
				if got := f2Meta(t, base, "vector_generation"); got != "1" {
					t.Errorf("vector_generation = %q, want 1", got)
				}
			} else {
				if !bytes.Equal(gotMain, newMain) || !bytes.Equal(gotDelta, newDelta) {
					t.Errorf("recovery must roll the whole pair forward (main %d bytes, delta %q)", len(gotMain), gotDelta)
				}
				if got := f2Meta(t, base, "vector_generation"); got != "2" {
					t.Errorf("vector_generation = %q, want 2", got)
				}
				if got := f2Meta(t, base, metaVectorMainGeneration); got != "2" {
					t.Errorf("%s = %q, want 2", metaVectorMainGeneration, got)
				}
			}
			if got := f2Meta(t, base, "pending_vector_file"); got != "" {
				t.Errorf("pending_vector_file after recovery = %q, want empty", got)
			}
			for _, tmp := range []string{".index.tmp", ".delta.tmp"} {
				if _, err := os.Stat(base + tmp); !os.IsNotExist(err) {
					t.Errorf("staging file %s survived recovery: %v", tmp, err)
				}
			}
		})
	}
}

func TestPendingVectorFilesList(t *testing.T) {
	if got := splitPendingFiles("idx.index.tmp"); len(got) != 1 || got[0] != "idx.index.tmp" {
		t.Errorf("a single name is the one-element list, got %v", got)
	}
	v := joinPendingFiles([]string{"a.index.tmp", "a.delta.tmp"})
	if got := splitPendingFiles(v); strings.Join(got, ",") != "a.index.tmp,a.delta.tmp" {
		t.Errorf("round trip = %v", got)
	}
}

// A tampered pending value must not rename anything outside the index
// directory.
func TestRecoverRejectsPathInPendingFile(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	st, err := openFormat2Store(base)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetMeta(ctx, "pending_vector_file", "../evil.tmp"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(ctx, "pending_vector_generation", "2"); err != nil {
		t.Fatal(err)
	}
	if err := recoverVectorPublication(ctx, st, base); err == nil {
		t.Fatal("a staged name that is a path must be refused")
	}
}
