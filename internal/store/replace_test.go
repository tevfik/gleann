package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// ReplaceAll is one transaction: another connection sees the old passages
// while it runs and the new ones after the commit, and a failed fill leaves
// the old state untouched.
func TestReplaceAllIsAtomicForReaders(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "idx.db")
	w, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	applyTexts(t, w, "old.go", []string{"old one", "old two"})

	r, err := Open(path, Options{}) // a second handle, as another process would have
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	seenDuring := -1
	err = w.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
		for i := 0; i < 5; i++ {
			id := int64(100 + i)
			text := fmt.Sprintf("new %d", i)
			if err := add(SourcedPassage{Source: "new.md", NewPassage: NewPassage{ID: &id, Text: text, Kind: "prose", Tokens: text}}); err != nil {
				return err
			}
		}
		n, err := r.Count(ctx)
		if err != nil {
			return err
		}
		seenDuring = n
		return nil
	}, map[string]string{"passage_count": "5"})
	if err != nil {
		t.Fatal(err)
	}
	if seenDuring != 2 {
		t.Errorf("reader saw %d passages during the replacement, want the old 2", seenDuring)
	}
	if n, _ := r.Count(ctx); n != 5 {
		t.Errorf("reader sees %d passages after commit, want 5", n)
	}
	if p, err := r.Get(ctx, 103); err != nil || p.Text != "new 3" || p.ChunkNo != 3 {
		t.Errorf("explicit id 103 = %+v, %v", p, err)
	}
	if lexCount(t, r, "new") != 5 || lexCount(t, r, "old") != 0 {
		t.Error("lex rows not replaced")
	}

	// A failing fill rolls back to the state before it.
	boom := errors.New("boom")
	err = w.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
		id := int64(1)
		if err := add(SourcedPassage{Source: "x.go", NewPassage: NewPassage{ID: &id, Text: "x", Tokens: "x"}}); err != nil {
			return err
		}
		return boom
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("ReplaceAll error = %v, want boom", err)
	}
	if n, _ := r.Count(ctx); n != 5 {
		t.Errorf("failed replacement changed the passages: %d, want 5", n)
	}
}
