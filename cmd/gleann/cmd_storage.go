package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/gleann"
)

const storageUsage = `Usage: gleann index storage [<name>] [--set store|recompute] [--index-dir <dir>]

Show or change how an index keeps its vectors.

  store       vectors are kept in the index file. A search embeds only the query
              (milliseconds). Disk: about passages x dims x 4 bytes extra.
  recompute   LEANN style: no vectors on disk, the neighbours visited by a search
              are re-embedded every time (seconds per query, hundreds of embedding
              calls). Smallest index.

Without --set the command lists the indexes with their mode and sizes.
--set rebuilds the vector index from the stored passages (embeddings come from
the embedding cache where possible); the source files are not needed. If the
rebuild fails the previous index is restored. The DiskANN backend always stores
vectors and cannot be switched.

  gleann index storage                         # table of all indexes
  gleann index storage docs --set recompute    # shrink one index
  gleann index storage docs --set store        # make it fast again`

func cmdStorage(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Println(storageUsage)
		return
	}
	config := getConfig(args)
	applySavedConfig(&config, args)

	name := ""
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		name = args[0]
	}
	target := getFlag(args, "--set")

	if target == "" {
		printStorageTable(config.IndexDir, name)
		return
	}
	if name == "" {
		fatalToken(fmt.Errorf("usage: gleann index storage <name> --set store|recompute"))
	}
	if err := setStorageMode(config, name, target, getFlag(args, "--progress")); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

func humanBytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f < k:
		return fmt.Sprintf("%d B", n)
	case f < k*k:
		return fmt.Sprintf("%.1f KB", f/k)
	case f < k*k*k:
		return fmt.Sprintf("%.1f MB", f/k/k)
	}
	return fmt.Sprintf("%.2f GB", f/k/k/k)
}

func printStorageTable(indexDir, only string) {
	metas, err := gleann.ListIndexes(indexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "INDEX\tBACKEND\tPASSAGES\tDIMS\tMODE\tINDEX FILE\tIF STORED\tNOTE")
	for _, m := range metas {
		if only != "" && m.Name != only {
			continue
		}
		vs := gleann.InspectVectorStorage(indexDir, m)
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\n", m.Name, m.Backend, m.NumPassages, m.Dimensions,
			vs.Mode, humanBytes(vs.IndexBytes), humanBytes(vs.FullBytes), vs.Note)
	}
	tw.Flush()
}

func setStorageMode(config gleann.Config, name, target, progress string) error {
	if target != gleann.StorageStore && target != gleann.StorageRecompute {
		return fmt.Errorf("--set must be %s or %s", gleann.StorageStore, gleann.StorageRecompute)
	}
	meta, err := gleann.GetIndexMeta(config.IndexDir, name)
	if err != nil {
		return err
	}
	vs := gleann.InspectVectorStorage(config.IndexDir, *meta)
	if !vs.Convertible {
		return fmt.Errorf("index %q (%s backend, mode %s) cannot be switched: %s", name, meta.Backend, vs.Mode, vs.Note)
	}
	if vs.Mode == target {
		fmt.Printf("index %q already uses %s storage\n", name, target)
		return nil
	}

	// Keep a copy of the files the rebuild rewrites; restore them on failure.
	dir := filepath.Join(config.IndexDir, name)
	bak, err := os.MkdirTemp(dir, ".storage-bak-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(bak)
	saved := backupIndexFiles(dir, name, bak)

	config.StorageMode = target
	if err := config.ApplyStorageMode(); err != nil {
		return err
	}
	if target == gleann.StorageStore {
		config.HNSWConfig.PruneKeepFraction = 1
	}
	config.Backend = meta.Backend
	config.EmbeddingModel = meta.EmbeddingModel
	config.EmbedderResolver = defaultEmbedderResolver(config)

	start := time.Now()
	opts := stdOptions(config, "all", true, false)
	opts.ModelExplicit = true
	res, err := indexing.Repair(context.Background(), indexing.RepairOptions{
		Options:  opts,
		Name:     name,
		Force:    true,
		Progress: embedProgress(progress, name),
	})
	if err != nil {
		restoreIndexFiles(dir, name, bak, saved)
		return fmt.Errorf("%w (previous index files restored)", err)
	}
	after, _ := gleann.GetIndexMeta(config.IndexDir, name)
	if after != nil {
		a := gleann.InspectVectorStorage(config.IndexDir, *after)
		fmt.Printf("✅ %s: %s -> %s, index file %s -> %s (%s)\n", name, vs.Mode, a.Mode,
			humanBytes(vs.IndexBytes), humanBytes(a.IndexBytes), time.Since(start).Round(time.Millisecond))
		if res != nil && res.CacheLookups > 0 {
			fmt.Printf("   embedding cache: %d/%d hits\n", res.CacheHits, res.CacheLookups)
		}
	}
	return nil
}

// storageBackupSuffixes are the files a storage switch rewrites. For format 2
// the database travels with its WAL files: a copy of <name>.db alone would miss
// what is still in the -wal.
func storageBackupSuffixes() []string {
	return []string{".index", ".meta.json", ".manifest.json", ".db", ".db-wal", ".db-shm"}
}

// backupIndexFiles copies the files a storage switch rewrites into bak and
// returns the suffixes it saved.
func backupIndexFiles(dir, name, bak string) []string {
	var saved []string
	for _, suffix := range storageBackupSuffixes() {
		if err := copyFile(filepath.Join(dir, name+suffix), filepath.Join(bak, name+suffix)); err == nil {
			saved = append(saved, suffix)
		}
	}
	return saved
}

// restoreIndexFiles puts the saved copies back. A -wal/-shm file that did not
// exist at backup time is removed, so the restored database is not paired with
// a log of the failed attempt.
func restoreIndexFiles(dir, name, bak string, saved []string) {
	have := make(map[string]bool, len(saved))
	for _, suffix := range saved {
		have[suffix] = true
	}
	if have[".db"] {
		for _, suffix := range []string{".db-wal", ".db-shm"} {
			if !have[suffix] {
				_ = os.Remove(filepath.Join(dir, name+suffix))
			}
		}
	}
	for _, suffix := range saved {
		_ = copyFile(filepath.Join(bak, name+suffix), filepath.Join(dir, name+suffix))
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
