package indexing

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// A PATCH (UpdateIndexMeta) that lands while RebuildIndex embeds, after the
// builder wrote meta.json and before the pipeline re-applies the user fields,
// must survive: the pipeline re-applied the snapshot taken at its start.
func TestRebuildKeepsAPatchMadeDuringTheBuild(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "a.txt"), "The margay is a small wild cat of the forests.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "rp", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	if err := gleann.UpdateIndexMeta(env.cfg.IndexDir, "rp", func(m *gleann.IndexMeta) { m.Description, m.Tags = "old", []string{"old"} }); err != nil {
		t.Fatal(err)
	}
	// New text: the embedding cache cannot answer it, so the embedder runs
	// (and reports progress) after the builder wrote meta.json.
	writeFile(t, filepath.Join(docs, "a.txt"), "The ocelot is a spotted wild cat that hunts at night.\n")
	var fired atomic.Bool
	opts := env.options(nil)
	opts.Progress = func(p Progress) {
		if fired.CompareAndSwap(false, true) {
			if err := gleann.UpdateIndexMeta(env.cfg.IndexDir, "rp", func(m *gleann.IndexMeta) { m.Description = "patched" }); err != nil {
				t.Errorf("PATCH during the build: %v", err)
			}
		}
	}
	if _, err := RebuildIndex(context.Background(), "rp", docs, opts); err != nil {
		t.Fatal(err)
	}
	if !fired.Load() {
		t.Fatal("the progress seam never fired (no embedding progress)")
	}
	m, err := gleann.GetIndexMeta(env.cfg.IndexDir, "rp")
	if err != nil {
		t.Fatal(err)
	}
	if m.Description != "patched" {
		t.Errorf("description = %q, want the PATCH made during the build", m.Description)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "old" {
		t.Errorf("tags = %v, want the untouched value carried over", m.Tags)
	}
}

// Sync's rebuild swaps the index directory. The rebuilt index carries the user
// fields of the old one from its first meta.json on (so nothing reads as
// untagged or exposed meanwhile); a PATCH after that, including one that clears
// a field, must survive the restore at the end.
func TestWithIndexRollbackKeepsAPatchMadeDuringTheRebuild(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "a.txt"), "The margay is a small wild cat of the forests.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "sw", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	dir := env.cfg.IndexDir
	if err := gleann.UpdateIndexMeta(dir, "sw", func(m *gleann.IndexMeta) { m.Description, m.Tags = "old", []string{"old"} }); err != nil {
		t.Fatal(err)
	}
	err := withIndexRollbackMeta(dir, "sw", newPrinter(io.Discard, io.Discard), func(prev *gleann.IndexMeta) error {
		if prev == nil || prev.Description != "old" {
			t.Errorf("prev = %+v, want the metadata from before the rewrite", prev)
		}
		// the rebuild's first meta.json, as BuildPassages writes it with SetPreviousMeta
		if err := os.MkdirAll(filepath.Join(dir, "sw"), 0o755); err != nil {
			return err
		}
		fresh := gleann.IndexMeta{Name: "sw"}
		gleann.MergeUserMeta(&fresh, prev, prev, prev)
		data, _ := json.Marshal(fresh)
		if err := os.WriteFile(filepath.Join(dir, "sw", "sw.meta.json"), data, 0o644); err != nil {
			return err
		}
		return gleann.UpdateIndexMeta(dir, "sw", func(m *gleann.IndexMeta) { m.Tags = []string{"patched"}; m.Description = "" })
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := gleann.GetIndexMeta(dir, "sw")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "patched" {
		t.Errorf("tags = %v, want the PATCH made during the rebuild", m.Tags)
	}
	if m.Description != "" {
		t.Errorf("description = %q, want the PATCH that cleared it", m.Description)
	}
}

// During the embedding phase of a sync full rebuild the index must already
// carry its user fields: an index set to mcp_exposed=false must not read as
// exposed, and its tags must match filters, until the rollback step restores them.
func TestSyncRebuildCarriesUserFieldsFromTheFirstMeta(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "a.txt"), "The margay is a small wild cat of the forests.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "sx", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	dir := env.cfg.IndexDir
	if err := gleann.UpdateIndexMeta(dir, "sx", func(m *gleann.IndexMeta) {
		m.Tags, m.Description, m.MCPExposed = []string{"team"}, "kept", func() *bool { b := false; return &b }()
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docs, "a.txt"), "The ocelot is a spotted wild cat that hunts at night.\n")

	var seen atomic.Bool
	opts := env.options(nil)
	opts.Progress = func(Progress) {
		if !seen.CompareAndSwap(false, true) {
			return
		}
		m, err := gleann.GetIndexMeta(dir, "sx")
		if err != nil {
			t.Errorf("meta during the rebuild: %v", err)
			return
		}
		if m.IsMCPExposed() || len(m.Tags) != 1 || m.Tags[0] != "team" || m.Description != "kept" {
			t.Errorf("during the rebuild: exposed=%v tags=%v description=%q, want the user fields already set", m.IsMCPExposed(), m.Tags, m.Description)
		}
	}
	ctx := context.Background()
	penv, cleanup, err := newPipelineEnv(ctx, &opts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	err = withIndexRollbackMeta(dir, "sx", penv.p, func(prev *gleann.IndexMeta) error {
		penv.prevMeta = prev
		_, rerr := penv.rebuildWith(ctx, "sx", docs, false, true)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seen.Load() {
		t.Fatal("no embedding progress was reported")
	}
	m, err := gleann.GetIndexMeta(dir, "sx")
	if err != nil || m.IsMCPExposed() || len(m.Tags) != 1 || m.Description != "kept" {
		t.Errorf("after the rebuild: %+v, %v", m, err)
	}
}

// A PATCH between the read of the old metadata and the move of the directory
// must not be lost with the rollback copy: the metadata is read from the moved
// copy. Simulated by a PATCH made right before the call (the read is after the
// move now); the previous code read before the move, so this documents the order.
func TestWithIndexRollbackReadsTheMetadataFromTheMovedCopy(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "a.txt"), "The margay is a small wild cat of the forests.\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "sn", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	dir := env.cfg.IndexDir
	bak := SyncBackupPath(dir, "sn")
	if err := gleann.UpdateIndexMeta(dir, "sn", func(m *gleann.IndexMeta) { m.Description = "from the copy" }); err != nil {
		t.Fatal(err)
	}
	err := withIndexRollbackMeta(dir, "sn", newPrinter(io.Discard, io.Discard), func(prev *gleann.IndexMeta) error {
		onDisk, rerr := os.ReadFile(filepath.Join(bak, "sn.meta.json"))
		if rerr != nil {
			t.Errorf("the rollback copy has no meta.json: %v", rerr)
		}
		var cp gleann.IndexMeta
		_ = json.Unmarshal(onDisk, &cp)
		if prev == nil || prev.Description != cp.Description || cp.Description != "from the copy" {
			t.Errorf("prev = %+v, copy = %+v: prev must be what the moved copy holds", prev, cp)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
