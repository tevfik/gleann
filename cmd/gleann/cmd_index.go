package main

import (
	"fmt"
	"os"
)

// cmdIndex dispatches index management subcommands:
//
//	gleann index list
//	gleann index build <name> --docs <dir>
//	gleann index remove <name>
//	gleann index rebuild <name> --docs <dir>
//	gleann index info <name>
//	gleann index watch <name> --docs <dir>
func cmdIndex(args []string) {
	if len(args) < 1 {
		printIndexUsage()
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

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
	case "help", "--help", "-h":
		printIndexUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown index subcommand: %s\n", sub)
		printIndexUsage()
		os.Exit(1)
	}
}

func printIndexUsage() {
	fmt.Println(`gleann index — manage vector indexes

Usage:
  gleann index list                         List all indexes
  gleann index build  <name> --docs <dir>   Build index from documents
  gleann index remove <name>                Remove an index
  gleann index rebuild <name> --docs <dir>  Remove & rebuild index from scratch
  gleann index info   <name>                Show index metadata
  gleann index watch  <name> --docs <dir>   Watch & auto-rebuild on changes

Options:
  --json                  Output as JSON (list, info)
  --graph                 Build AST-based code graph (build, rebuild)
  --docs <dir>            Source directory (required for build/rebuild/watch)
  --index-dir <dir>       Index storage directory (default: ~/.gleann/indexes)
  --prune                 Prune unchanged files during incremental builds
  --multimodal-model <m>  Model for media file indexing (images/audio/video)
                          Auto-detects if GLEANN_MULTIMODAL_MODEL is set

Examples:
  gleann index list
  gleann index build my-docs --docs ./documents/
  gleann index build my-code --docs ./src/ --graph
  gleann index build my-media --docs ./media/ --multimodal-model gemma4:e4b
  gleann index remove my-old-index
  gleann index rebuild my-code --docs ./src/ --graph
  gleann index info my-docs
  gleann index watch my-code --docs ./src/`)
}
