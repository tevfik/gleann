package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/modules/settings"
)

// cmdModels lists and downloads the local GGUF models of the catalog
// (internal/modelcatalog/models.json), the same list the web UI and
// `gleann setup` offer.
func cmdModels(args []string) {
	if len(args) == 0 || args[0] == "list" {
		fmt.Printf("Models for the llamacpp and eif providers (stored in %s):\n\n", modelcatalog.Dir())
		kind := ""
		for _, e := range modelcatalog.All() {
			if e.Kind != kind {
				kind = e.Kind
				fmt.Printf("%s\n", strings.ToUpper(kind))
			}
			state := " "
			if e.Installed() {
				state = "✓"
			}
			fmt.Printf("  %s %-28s %-40s %s\n", state, e.ID, e.Name, settings.HumanSize(e.Bytes))
		}
		fmt.Println("\nDownload one with: gleann models download <id>")
		return
	}
	if args[0] != "download" || len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann models [list | download <id>...]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	failed := false
	for _, id := range args[1:] {
		e, ok := modelcatalog.Find(id)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown model %q (see: gleann models list)\n", id)
			failed = true
			continue
		}
		if e.Installed() {
			fmt.Printf("✓ %s is already in %s\n", e.Name, e.Path())
			continue
		}
		fmt.Printf("⬇  %s (%s)\n", e.Name, settings.HumanSize(e.Bytes))
		err := modelcatalog.Download(ctx, e, func(done, total int64) {
			if total > 0 {
				fmt.Printf("\r   %3d%%  %d / %d MB   ", done*100/total, done>>20, total>>20)
			}
		})
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			failed = true
			continue
		}
		fmt.Printf("✓ %s\n", e.Path())
	}
	if failed {
		os.Exit(1)
	}
}
