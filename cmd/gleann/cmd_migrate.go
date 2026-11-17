package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

func printMigrateUsage() {
	fmt.Println(`Usage: gleann index migrate <name> [--language <l>]
       gleann index migrate --all [--language <l>]
       gleann index migrate <name>|--all --rollback
       gleann index migrate <name>|--all --cleanup

Convert an index's passage store from the bbolt format (format 1) to the
SQLite record store (format 2, RFC 003). The vector file is not touched and
no embeddings are computed: passages are copied with their old IDs as vector
slots, verified, and switched atomically under the index lock. The old files
are preserved as <name>.passages.db.v1 and <name>.manifest.json.v1, and guard
files at the old names make older gleann binaries fail loudly instead of
reading an empty store.

Options:
  --all          Migrate every index instead of one.
  --language <l> Analyzer language for the lexical rows (default: guessed
                 from the corpus; 'tr' enables the Turkish prose rules).
  --rollback     Restore the preserved format-1 files (byte-identical) and
                 delete the format-2 store. Changes synced after the switch
                 are lost.
  --cleanup      Delete the preserved .v1 files, making the switch permanent
                 (rollback is no longer possible).
  --index-dir <dir>  Index storage directory (default: ~/.gleann/indexes)
  -h, --help     Show this help message`)
}

// cmdMigrate implements `gleann index migrate <name>|--all [--rollback]
// [--cleanup] [--language <l>]` (RFC 003 section 6.4).
func cmdMigrate(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		printMigrateUsage()
		return
	}
	all := hasFlag(args, "--all")
	rollback := hasFlag(args, "--rollback")
	cleanup := hasFlag(args, "--cleanup")
	language := getFlag(args, "--language")
	if rollback && cleanup {
		fmt.Fprintln(os.Stderr, "usage: --rollback and --cleanup cannot be combined; roll back first, then migrate again if needed")
		exitFunc(1)
		return
	}
	if language != "" && (rollback || cleanup) {
		fmt.Fprintln(os.Stderr, "usage: --language only applies to a migration, not to --rollback or --cleanup")
		exitFunc(1)
		return
	}
	if len(args) == 0 || (!all && strings.HasPrefix(args[0], "-")) {
		fmt.Fprintln(os.Stderr, "usage: gleann index migrate <name> [--language <l>]  |  gleann index migrate --all [--rollback] [--cleanup]")
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
		var err error
		switch {
		case rollback:
			err = gleann.RollbackMigration(config.IndexDir, name)
			if err == nil {
				fmt.Printf("✅ %s: rolled back to store format 1\n", name)
			}
		case cleanup:
			err = gleann.CleanupMigration(config.IndexDir, name)
			if err == nil {
				fmt.Printf("✅ %s: removed the preserved format-1 files; rollback is no longer possible\n", name)
			}
		default:
			// An already-migrated index is a skip, not a failure, so --all can
			// be re-run safely after a partial migration. The meta flag must
			// agree with the files: a crash between the database rename and
			// the flag leaves files that look format 2, and MigrateIndex
			// completes that switch instead of skipping it.
			if gleann.StoreFormatOf(filepath.Join(config.IndexDir, name, name)) == 2 {
				if m, err := gleann.GetIndexMeta(config.IndexDir, name); err == nil && m.StoreFormat == 2 {
					fmt.Printf("✅ %s: already store format 2\n", name)
					continue
				}
			}
			err = gleann.MigrateIndex(config.IndexDir, name, gleann.MigrateOptions{Language: language})
			if err == nil {
				fmt.Printf("✅ %s: migrated to store format 2 (undo: gleann index migrate %s --rollback)\n", name, name)
			}
		}
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", name, err)
		}
	}
	if failed > 0 {
		exitFunc(1)
	}
}
