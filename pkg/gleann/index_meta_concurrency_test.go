package gleann

import (
	"errors"
	"io/fs"

	"fmt"
	"github.com/tevfik/gleann/internal/metafault"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestUpdateIndexMetaConcurrent: parallel updates of one index must all
// succeed (no shared temp file to lose a rename on), none may be lost, and the
// metadata file stays valid JSON.
func TestUpdateIndexMetaConcurrent(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, "idx", `{"name":"idx"}`)

	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- UpdateIndexMeta(dir, "idx", func(m *IndexMeta) {
				m.Tags = append(m.Tags, fmt.Sprintf("t%d", i))
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update failed: %v", err)
		}
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil {
		t.Fatalf("metadata unreadable after concurrent updates: %v", err)
	}
	if len(m.Tags) != n {
		t.Errorf("lost updates: %d of %d tags survived", len(m.Tags), n)
	}
}

// The lock must not depend on how the index directory is spelled: updates
// through a symlink alias and through the real path serialise together.
func TestUpdateIndexMetaConcurrentThroughSymlink(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeMeta(t, real, "idx", `{"name":"idx"}`)

	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := real
			if i%2 == 1 {
				d = alias
			}
			errs <- UpdateIndexMeta(d, "idx", func(m *IndexMeta) { m.Tags = append(m.Tags, fmt.Sprintf("t%d", i)) })
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("update failed: %v", err)
		}
	}
	m, err := GetIndexMeta(real, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tags) != n {
		t.Errorf("lost updates through the alias: %d of %d tags survived", len(m.Tags), n)
	}
}

// Write-phase failures carry ErrIndexMetaWrite (so callers can tell them from
// a missing index); a missing index does not.
func TestUpdateIndexMetaResultErrorKinds(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, "idx", `{"name":"idx"}`)

	if _, err := UpdateIndexMetaResult(dir, "ghost", func(*IndexMeta) {}); err == nil || errors.Is(err, ErrIndexMetaWrite) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing index: %v, want a plain not-exist error", err)
	}
	t.Cleanup(metafault.Set(func(string) error { return fs.ErrNotExist }))
	if _, err := UpdateIndexMetaResult(dir, "idx", func(*IndexMeta) {}); !errors.Is(err, ErrIndexMetaWrite) {
		t.Errorf("write failure: %v, want ErrIndexMetaWrite", err)
	}
	metafault.Set(nil)
	m, err := UpdateIndexMetaResult(dir, "idx", func(m *IndexMeta) { m.Description = "d" })
	if err != nil || m.Description != "d" {
		t.Errorf("result = %+v, %v", m, err)
	}
}
