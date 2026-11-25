package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

// Item 2 (wave v122): `memory list` and `memory search` print the first 8
// characters of a block ID, so Manager.Forget must accept a unique ID prefix as
// well as a full ID, and must refuse an ambiguous prefix instead of falling
// through to the phrase delete.

func newForgetStore(t *testing.T) *Manager {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mgr := NewManager(store)
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

// putBlockWithID stores a long-term block under an explicit ID so the test can
// choose IDs that share a prefix.
func putBlockWithID(t *testing.T, mgr *Manager, id, content string) {
	t.Helper()
	b, err := mgr.RememberBlock(&Block{ID: id, Tier: TierLong, Label: "note", Content: content, Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != id {
		t.Fatalf("block stored under %q, want %q", b.ID, id)
	}
}

func TestForgetIDPrefixUnique(t *testing.T) {
	cases := []struct{ id, prefix, content string }{
		{"a1b2c3d4e5f60718", "a1b2c3d4", "the build gate is go vet"},
		{"b2c3d4e5f607189a", "b2c3d4e5", "changelog bullets go under Unreleased"},
		{"c3d4e5f607189a1b", "c3d4e5f607189a1", "one writer per index database"},
		{"d4e5f607189a1b2c", "D4E5F607", "kuzu was dropped in v1.20"},
	}
	mgr := newForgetStore(t)
	for _, c := range cases {
		putBlockWithID(t, mgr, c.id, c.content)
	}

	for _, c := range cases {
		n, err := mgr.Forget(c.prefix)
		if err != nil {
			t.Fatalf("Forget(%q) = %v, want 1 deleted", c.prefix, err)
		}
		if n != 1 {
			t.Errorf("Forget(%q) = %d blocks, want 1", c.prefix, n)
		}
	}
	if blocks, err := mgr.store.List(""); err != nil {
		t.Fatal(err)
	} else if len(blocks) != 0 {
		t.Errorf("remaining blocks = %v, want none", blocks)
	}
}

func TestForgetIDPrefixAmbiguous(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "ffff0000aaaaffff", "first fact")
	putBlockWithID(t, mgr, "ffff0000bbbbffff", "second fact")

	n, err := mgr.Forget("ffff0000")
	if err == nil {
		t.Fatalf("Forget of an ambiguous prefix deleted %d blocks, want an error", n)
	}
	if n != 0 {
		t.Errorf("Forget = %d, want 0 with the ambiguity error", n)
	}
	if !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "2 blocks") {
		t.Errorf("error = %q, want it to mention 2 ambiguous blocks", err)
	}
	// The ambiguity must not fall through to the phrase delete.
	if blocks, err := mgr.store.List(""); err != nil {
		t.Fatal(err)
	} else if len(blocks) != 2 {
		t.Errorf("ambiguous prefix removed blocks: %v", blocks)
	}
}

func TestForgetIDPrefixNoMatchFallsBackToPhrase(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "a1b2c3d4e5f60718", "the abcd constant marks a rollback point")

	// "abcd" is a valid hex prefix with no matching ID, so it is still a
	// phrase query and must delete the block that contains the word.
	n, err := mgr.Forget("abcd")
	if err != nil {
		t.Fatalf("Forget(abcd) = %v", err)
	}
	if n != 1 {
		t.Errorf("Forget(abcd) = %d, want 1", n)
	}

	// A short or non-hex query never enters the prefix path.
	if _, err := mgr.Forget("ab"); err == nil {
		t.Error("Forget(ab) matched something, want no match")
	}
	if _, err := mgr.Forget("zzz"); err == nil {
		t.Error("Forget(zzz) matched something, want no match")
	}
}

func TestForgetIDPrefixTooShortStaysPhrase(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "a1b2c3d4e5f60718", "the ab ab constant")

	// "a1" is hex but shorter than the minimum prefix length: Forget must not
	// delete by prefix, and the phrase match ("ab ab") proves the fall-through.
	if _, err := mgr.Forget("a1"); err == nil {
		t.Error("Forget(a1) deleted by prefix, want no match")
	}
	if _, err := mgr.Forget("ab ab"); err != nil {
		t.Errorf("phrase forget after a short prefix failed: %v", err)
	}
}

// Second review F1: a hex-looking content word must never delete the block
// whose ID happens to start with it.
func TestForgetHexContentWordDoesNotDeleteByShortPrefix(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "2024aaaaaaaaaaaa", "unrelated architecture decision")
	putBlockWithID(t, mgr, "bbbbbbbbbbbbbbbb", "release notes for 2024")

	for _, q := range []string{"2024", "2024aa", "2024aaa"} {
		if _, err := mgr.Forget(q); err == nil {
			// "2024" matches content of block b; "2024aa*" matches nothing.
			if q != "2024" {
				t.Errorf("Forget(%q) succeeded, want no match", q)
			}
		}
	}
	blocks, _ := mgr.store.List("")
	for _, b := range blocks {
		if b.ID == "2024aaaaaaaaaaaa" {
			return
		}
	}
	t.Fatal("block 2024aaaa... (unrelated) was deleted by a content word")
}

// An 8+ hex argument matching both an ID prefix and block content is refused.
func TestForgetPrefixAndContentBothMatchIsRefused(t *testing.T) {
	mgr := newForgetStore(t)
	putBlockWithID(t, mgr, "deadbeef00000001", "unrelated decision")
	putBlockWithID(t, mgr, "bbbbbbbbbbbbbbbb", "the marker deadbeef is a magic value")

	n, err := mgr.Forget("deadbeef")
	if err == nil || n != 0 {
		t.Fatalf("Forget(deadbeef) = %d, %v; want refusal", n, err)
	}
	if !strings.Contains(err.Error(), "full 16-character ID") {
		t.Errorf("error should say to pass the full ID: %v", err)
	}
	if blocks, _ := mgr.store.List(""); len(blocks) != 2 {
		t.Errorf("a block was deleted: %v", blocks)
	}
	// The full ID still works.
	if n, err := mgr.Forget("deadbeef00000001"); err != nil || n != 1 {
		t.Errorf("full ID forget = %d, %v", n, err)
	}
}
