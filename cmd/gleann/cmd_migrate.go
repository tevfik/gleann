package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/gleann"
)

func printMigrateUsage() {
	fmt.Println(`Usage: gleann index migrate <name> [--graph] [--cleanup]
       gleann index migrate --all [--graph] [--cleanup]

Convert an index in the old store format (gleann v1.9 and earlier: bbolt
passages, <name>_graph code graph) to the SQLite index database that v1.20
uses. Passages are copied and the vectors reused, so no embeddings are
computed; passage IDs with gaps are renumbered. When the old index had a code
graph, the graph is rebuilt from the index's source directory. A
<name>_memory_vectors index is converted too. Every other command refuses an
index in the old format until it is migrated (or rebuilt with
'gleann index rebuild <name>').

The old files are kept; --cleanup deletes them after a successful
conversion and prints each deleted path. 'gleann doctor' lists them.

Options:
  --all          Migrate every index instead of one.
  --graph        Build the code graph even if the old index had none.
  --cleanup      Delete the converted index's old files.
  --index-dir <dir>  Index storage directory (default: ~/.gleann/indexes)
  -h, --help     Show this help message`)
}

// cmdMigrate implements `gleann index migrate <name>|--all [--graph]
// [--cleanup]` (RFC 004 B3).
func cmdMigrate(args []string) {
	if wantsHelpIn(args, indexFlagSets["migrate"]) {
		printMigrateUsage()
		return
	}
	all := hasFlag(args, "--all")
	cleanup := hasFlag(args, "--cleanup")
	graph := hasFlag(args, "--graph")
	if hasFlag(args, "--rollback") {
		fmt.Fprintln(os.Stderr, "error: --rollback was removed in v1.20; the old files stay next to the index until 'gleann index migrate <name> --cleanup'")
		exitFunc(1)
		return
	}
	if getFlag(args, "--language") != "" {
		fmt.Fprintln(os.Stderr, "error: --language was removed in v1.20; a migrated index uses the same analyzer as a fresh build")
		exitFunc(1)
		return
	}
	if len(args) == 0 || (!all && strings.HasPrefix(args[0], "-")) {
		fmt.Fprintln(os.Stderr, "usage: gleann index migrate <name>|--all [--graph] [--cleanup]")
		exitFunc(1)
		return
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	var names []string
	if all {
		metas, err := gleann.ListIndexes(config.IndexDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot list indexes in %s: %v\n", config.IndexDir, err)
			exitFunc(1)
			return
		}
		for _, m := range metas {
			// A memory vector index is converted with its owner.
			if owner := strings.TrimSuffix(m.Name, "_memory_vectors"); owner != m.Name && containsName(metas, owner) {
				continue
			}
			names = append(names, m.Name)
		}
		if len(names) == 0 {
			fmt.Printf("No indexes found in %s.\n", config.IndexDir)
			return
		}
	} else {
		names = []string{args[0]}
	}

	failed := 0
	for _, name := range names {
		opts := stdOptions(config, "", false, false)
		res, err := indexing.Migrate(context.Background(), indexing.MigrateOptions{Options: opts, Name: name, Graph: graph})
		if res != nil {
			switch {
			case res.AlreadyFormat2:
				fmt.Printf("✅ %s: already store format 2\n", name)
			case res.Store != nil:
				msg := fmt.Sprintf("✅ %s: migrated %d passages to store format 2 (vectors reused)", name, res.Store.Passages)
				if res.Store.Renumbered {
					msg += "; passage IDs with gaps were renumbered"
				}
				fmt.Println(msg)
			}
			if res.MemoryVectors != nil {
				fmt.Printf("✅ %s_memory_vectors: migrated %d passages\n", name, res.MemoryVectors.Passages)
			}
			if res.GraphBuilt {
				fmt.Printf("✅ %s: code graph rebuilt from the sources\n", name)
			}
		}
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", name, err)
			continue
		}
		if cleanup {
			for _, n := range []string{name, name + "_memory_vectors"} {
				if gleann.DetectStoreFormat(filepath.Join(config.IndexDir, n, n)) != gleann.StoreV2 {
					continue
				}
				removed, err := gleann.CleanupMigration(config.IndexDir, n)
				for _, p := range removed {
					fmt.Printf("🗑️  removed %s\n", p)
				}
				if err != nil {
					failed++
					fmt.Fprintf(os.Stderr, "❌ %s: cleanup: %v\n", n, err)
				}
			}
		} else if left := gleann.MigrationLeftovers(config.IndexDir, name); len(left) > 0 && res != nil && res.Store != nil {
			fmt.Printf("   The old files are kept (%d); delete them with: gleann index migrate %s --cleanup\n", len(left), name)
		}
	}
	if failed > 0 {
		exitFunc(1)
	}
}

func containsName(metas []gleann.IndexMeta, name string) bool {
	for _, m := range metas {
		if m.Name == name {
			return true
		}
	}
	return false
}
