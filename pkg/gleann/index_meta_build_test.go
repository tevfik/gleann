package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A full build writes <name>.meta.json while UpdateIndexMeta (the PATCH path)
// updates it. The build's write used to be a plain os.WriteFile outside the
// per-index lock: truncate-then-write, so a concurrent reader (or the update's
// own read) saw an empty or half-written file. Now both go through the same
// locked, atomic replace: the file is valid JSON at every instant, and no
// update fails.
func TestBuildMetaWriteAgainstConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, "idx", "idx.meta.json")

	var stop atomic.Bool
	var wg sync.WaitGroup
	var bad atomic.Value // first problem seen
	fail := func(format string, a ...any) { bad.CompareAndSwap(nil, fmt.Sprintf(format, a...)) }

	wg.Add(1)
	go func() { // raw reader: every observed state must be complete JSON
		defer wg.Done()
		for !stop.Load() {
			data, err := os.ReadFile(metaPath)
			if err != nil {
				fail("read: %v", err)
				return
			}
			var m IndexMeta
			if err := json.Unmarshal(data, &m); err != nil {
				fail("invalid JSON observed (%d bytes): %v", len(data), err)
				return
			}
			if m.Name != "idx" {
				fail("lost field: name = %q", m.Name)
				return
			}
		}
	}()
	for u := 0; u < 2; u++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Description = "patched" }); err != nil {
					fail("UpdateIndexMeta: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	for i := 0; i < 8 && bad.Load() == nil; i++ {
		if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
			fail("Build: %v", err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if v := bad.Load(); v != nil {
		t.Fatal(v)
	}
	if _, err := GetIndexMeta(dir, "idx"); err != nil {
		t.Fatalf("final metadata: %v", err)
	}
}

// replaceIndexMeta shares UpdateIndexMeta's lock: it waits for an update in
// progress instead of interleaving with it.
func TestReplaceIndexMetaTakesTheIndexLock(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, "idx", `{"name":"idx"}`)

	inUpdate := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- UpdateIndexMeta(dir, "idx", func(*IndexMeta) { close(inUpdate); <-release })
	}()
	<-inUpdate
	replaced := make(chan error, 1)
	go func() {
		replaced <- replaceIndexMeta(dir, "idx", func(*IndexMeta) (*IndexMeta, error) {
			return &IndexMeta{Name: "idx", Description: "built"}, nil
		})
	}()
	select {
	case err := <-replaced:
		t.Fatalf("replaceIndexMeta ran while an update held the lock (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-replaced; err != nil {
		t.Fatal(err)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil || m.Description != "built" {
		t.Fatalf("final = %+v, %v (the replace must land after the update)", m, err)
	}
}

// Temporary files a crashed update left behind are removed by the next update
// once they are an hour old; recent ones (a live update), other indexes' temp
// files and look-alike names stay.
func TestUpdateIndexMetaRemovesStaleTemps(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, "idx", `{"name":"idx"}`)
	idxDir := filepath.Join(dir, "idx")
	old := time.Now().Add(-2 * time.Hour)

	mk := func(name string, mod time.Time) string {
		p := filepath.Join(idxDir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	staleA := mk("idx.meta.json.123456.tmp", old)
	staleB := mk("idx.meta.json.9.tmp", old)
	fresh := mk("idx.meta.json.777.tmp", time.Now().Add(-5*time.Minute))
	keep := []string{
		fresh,
		mk("other.meta.json.123.tmp", old),       // another index
		mk("idx.meta.json.abc.tmp", old),         // not a CreateTemp random part
		mk("idx.meta.json.tmp", old),             // no random part
		mk("idx.meta.json.123.bak", old),         // other suffix
		mk("idx.meta.json.meta.json.5.tmp", old), // an index named idx.meta.json
		mk("idx.db", old),
	}

	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Description = "d" }); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{staleA, staleB} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale temp %s not removed (err=%v)", filepath.Base(p), err)
		}
	}
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must stay: %v", filepath.Base(p), err)
		}
	}
	if m, err := GetIndexMeta(dir, "idx"); err != nil || m.Description != "d" {
		t.Fatalf("meta after update = %+v, %v", m, err)
	}
}

func ptrBool(b bool) *bool { return &b }

// A PATCH that lands while a rebuild runs (after it read its snapshot, before
// it writes meta.json) must survive: for each user field the file's value wins
// when it differs from the snapshot, including an explicit empty description.
func TestBuildKeepsAPatchMadeDuringTheBuild(t *testing.T) {
	cases := []struct {
		name  string
		patch func(*IndexMeta)
		check func(*testing.T, *IndexMeta)
	}{
		{"description", func(m *IndexMeta) { m.Description = "patched" }, func(t *testing.T, m *IndexMeta) {
			if m.Description != "patched" {
				t.Errorf("description = %q, want patched", m.Description)
			}
		}},
		{"description cleared", func(m *IndexMeta) { m.Description = "" }, func(t *testing.T, m *IndexMeta) {
			if m.Description != "" {
				t.Errorf("description = %q, want the explicit empty value", m.Description)
			}
		}},
		{"tags", func(m *IndexMeta) { m.Tags = []string{"new"} }, func(t *testing.T, m *IndexMeta) {
			if len(m.Tags) != 1 || m.Tags[0] != "new" {
				t.Errorf("tags = %v", m.Tags)
			}
		}},
		{"mcp_exposed", func(m *IndexMeta) { m.MCPExposed = ptrBool(false) }, func(t *testing.T, m *IndexMeta) {
			if m.MCPExposed == nil || *m.MCPExposed {
				t.Errorf("mcp_exposed = %v", m.MCPExposed)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			b, _ := newFormat2Builder(t, dir, 8)
			if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
				t.Fatal(err)
			}
			if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) {
				m.Description, m.Tags, m.MCPExposed, m.AutoWatch = "old", []string{"old"}, ptrBool(true), true
			}); err != nil {
				t.Fatal(err)
			}
			patched := false
			// passages published, the build's meta.json not written yet.
			b.crashHook = func(point string) error {
				if point == crashBeforePassagesMeta && !patched {
					patched = true
					return UpdateIndexMeta(dir, "idx", tc.patch)
				}
				return nil
			}
			if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
				t.Fatal(err)
			}
			if !patched {
				t.Fatal("the test seam never fired")
			}
			m, err := GetIndexMeta(dir, "idx")
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, m)
			if !m.AutoWatch { // untouched fields still come from the snapshot
				t.Error("auto_watch lost: untouched fields must carry over")
			}
		})
	}
}

func TestMergeUserMeta(t *testing.T) {
	snap := &IndexMeta{Description: "s", Tags: []string{"a"}, SourceDir: "/s"}
	// unedited: the snapshot's values
	var dst IndexMeta
	MergeUserMeta(&dst, snap, snap, snap)
	if dst.Description != "s" || dst.SourceDir != "/s" || len(dst.Tags) != 1 {
		t.Errorf("unedited: %+v", dst)
	}
	// unreadable current: the snapshot
	dst = IndexMeta{}
	MergeUserMeta(&dst, snap, snap, nil)
	if dst.Description != "s" {
		t.Errorf("nil current: %+v", dst)
	}
	// zero baseline (fresh rebuild): a set field wins, an empty one takes the snapshot
	dst = IndexMeta{}
	MergeUserMeta(&dst, snap, nil, &IndexMeta{Description: "edited"})
	if dst.Description != "edited" || dst.SourceDir != "/s" || len(dst.Tags) != 1 {
		t.Errorf("zero baseline: %+v", dst)
	}
}
