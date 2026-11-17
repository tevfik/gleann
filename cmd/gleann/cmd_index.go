package main

import (
	"fmt"
	"os"

	"github.com/tevfik/gleann/pkg/gleann"
)

// cmdIndex dispatches index management subcommands:
//
//	gleann index list
//	gleann index build <name> --docs <dir>
//	gleann index remove <name>
//	gleann index rebuild <name> --docs <dir>
//	gleann index info <name>
//	gleann index repair <name>|--all [--check]
//	gleann index migrate <name>|--all [--graph] [--cleanup]
//	gleann index watch <name> --docs <dir>
func cmdIndex(args []string) {
	if len(args) < 1 {
		printIndexUsage()
		exitFunc(1)
	}

	sub := args[0]
	subArgs := args[1:]
	if fs, ok := indexFlagSets[sub]; ok {
		rejectUnknownFlags("index "+sub, subArgs, fs)
	}
	refuseOldFormat(sub, subArgs)

	switch sub {
	case "list":
		cmdList(subArgs)
	case "build":
		cmdBuild(subArgs)
	case "remove":
		cmdRemove(subArgs)
	case "rebuild":
		cmdRebuild(subArgs)
	case "info":
		cmdInfo(subArgs)
	case "watch":
		cmdWatch(subArgs)
	case "sync":
		cmdSync(subArgs)
	case "repair":
		cmdRepair(subArgs)
	case "migrate":
		cmdMigrate(subArgs)
	case "storage":
		cmdStorage(subArgs)
	case "tag":
		cmdTag(subArgs)
	case "set":
		cmdSet(subArgs)
	case "help", "--help", "-h":
		printIndexUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown index subcommand: %s\n", sub)
		printIndexUsage()
		exitFunc(1)
	}
}

// refuseOldFormat stops an index subcommand on an index in the old store
// format with the migration hint. Only migrate (which converts it), remove,
// rebuild (which deletes it first), list (which marks it) and build (which
// refuses on its own) handle such an index (RFC 004 B2).
func refuseOldFormat(sub string, args []string) {
	switch sub {
	case "info", "watch", "sync", "repair", "storage", "tag", "set":
	default:
		return
	}
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' {
		return
	}
	config := getConfig(args)
	applySavedConfig(&config, args)
	if err := gleann.CheckStoreFormat(config.IndexDir, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

func printIndexUsage() {
	fmt.Println(`gleann index — manage vector indexes

Usage:
  gleann index list                         List all indexes
  gleann index build  <name> --docs <dir>   Build index from documents
  gleann index sync   <name> [--docs <dir>] Sync index incrementally with workspace changes
  gleann index remove <name>                Remove an index
  gleann index repair <name>|--all [--check] [--force]  Check an index for damage and rebuild its vectors from stored passages
  gleann index migrate <name>|--all [--graph] [--cleanup]  Convert an index built by gleann v1.9 or earlier
  gleann index rebuild <name> [--docs <dir>]  Remove & rebuild index from scratch (source dir from the index)
  gleann index info   <name>                Show index metadata
  gleann index watch  <name> --docs <dir>   Watch & auto-rebuild on changes
  gleann index storage [<name>] [--set store|recompute]  Show/change how vectors are kept (fast vs. small)
  gleann index tag    <name> [--add <t>] [--remove <t>]  Manage index tags
  gleann index set    <name> [--mcp=true|false] [--desc <d>]  Configure MCP exposure & description

Options:
  --json                  Output as JSON (list, info)
  --graph                 Build AST-based code graph (build, rebuild, sync)
  --mode <code|docs|all>  Index mode: code (fast, source code only), docs (office docs only), all (default)
  --no-plugins            Disable external document extraction plugins
  --docs <dir>            Source directory (required for build/rebuild/watch)
  --index-dir <dir>       Index storage directory (default: ~/.gleann/indexes)
  --prune                 Prune unchanged files during incremental builds
  --multimodal-model <m>  Model for media file indexing (images/audio/video)
                          Auto-detects if GLEANN_MULTIMODAL_MODEL is set

Examples:
  gleann index list
  gleann index build my-docs --docs ./documents/
  gleann index build my-code --docs ./src/ --graph --mode code
  gleann index sync  my-code --mode code
  gleann index build my-media --docs ./media/ --multimodal-model gemma4:e4b
  gleann index remove my-old-index
  gleann index rebuild my-code --docs ./src/ --graph
  gleann index info my-docs
  gleann index watch my-code --docs ./src/`)
}
