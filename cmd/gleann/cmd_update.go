package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

const gleannRepoOwner, gleannRepoName = "tevfik", "gleann"

// cmdUpdate implements `gleann update [--check] [--force]`: it replaces the
// running gleann with the latest GitHub release of the same build variant.
func cmdUpdate(args []string) {
	check, force := false, false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "--force":
			force = true
		case "-h", "--help", "help":
			fmt.Println(`usage: gleann update [--check] [--force]

  gleann update           download the latest release and replace this binary
  gleann update --check   only report whether a newer release exists
  gleann update --force   reinstall even when already up to date (or a dev build)

Installed plugins are updated with: gleann plugin update`)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	rel, err := gleann.FetchLatestRelease(ctx, gleannRepoOwner, gleannRepoName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot check for updates: %v\n", err)
		os.Exit(1)
	}
	newer, comparable := gleann.IsNewer(version, rel.Tag)
	fmt.Printf("installed %s, latest %s\n", version, rel.Tag)
	switch {
	case !comparable && !force:
		fmt.Println("This is a development build; use --force to replace it with the release.")
		return
	case comparable && !newer && !force:
		fmt.Println("gleann is up to date.")
		return
	case check:
		fmt.Println("A newer release is available: run `gleann update`.")
		return
	}

	dest, variant, err := updateTarget()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot update: %v\n", err)
		os.Exit(1)
	}
	names := make([]string, len(rel.Assets))
	for i, a := range rel.Assets {
		names[i] = a.Name
	}
	idx, err := gleann.SelectGleannAsset(names, variant, rel.Tag, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot update: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Downloading %s...\n", rel.Assets[idx].Name)
	newPath, verified, err := gleann.DownloadReleaseBinary(ctx, rel, idx, variant, dest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
	if err := gleann.ReplaceBinary(dest, newPath); err != nil {
		_ = os.Remove(newPath)
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
	if verified {
		fmt.Println("Checksum verified.")
	} else {
		fmt.Println("Note: the release publishes no checksum file, so only HTTPS protected the download.")
	}
	fmt.Printf("Updated %s to %s.\n", dest, rel.Tag)
	fmt.Println("Restart running `gleann serve` / MCP processes to use the new version.")
}

// updateTarget returns the file to replace and the release variant to fetch.
// The full build runs from an extracted copy, so the self-extracting launcher
// (GLEANN_LAUNCHER) is the file to replace, not the running executable.
func updateTarget() (dest, variant string, err error) {
	if l := os.Getenv("GLEANN_LAUNCHER"); l != "" {
		return l, "gleann-full", nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if strings.Contains(filepath.Base(self), "gleann-full-bin") {
		return "", "", fmt.Errorf("running from an extracted runtime without its launcher; reinstall with scripts/install.sh")
	}
	return self, "gleann", nil
}

// updateReleasePlugins updates (or with check, reports on) every installed
// binary plugin of the catalog that matches names (all when empty). It returns
// the repository directory names it took care of.
func updateReleasePlugins(names []string, check, force bool) map[string]bool {
	handled := map[string]bool{}
	catalog := gleann.FetchPluginCatalog()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	for _, meta := range catalog {
		if len(names) > 0 {
			match := false
			for _, n := range names {
				if m, ok := catalogPluginFor(n, []gleann.PluginMeta{meta}); ok && m.Name == meta.Name {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if check {
			res, _, err := gleann.CheckPluginUpdate(ctx, meta)
			if err != nil {
				if !strings.Contains(err.Error(), "not installed") {
					fmt.Printf("[check]  %s: %v\n", meta.Name, err)
				}
				continue
			}
			handled[meta.Name] = true
			if res.Newer {
				fmt.Printf("[check]  %s: %s → %s available\n", meta.Name, orUnknown(res.Installed), res.Latest)
			} else {
				fmt.Printf("[check]  %s: up to date (%s)\n", meta.Name, res.Installed)
			}
			continue
		}
		progress := make(chan string, 16)
		done := make(chan struct{})
		go func() {
			for line := range progress {
				fmt.Println("[update]", line)
			}
			close(done)
		}()
		res, err := gleann.UpdatePlugin(ctx, meta, force, progress)
		close(progress)
		<-done
		if err != nil {
			if !strings.Contains(err.Error(), "not installed") {
				fmt.Fprintf(os.Stderr, "[update] %s failed: %v\n", meta.Name, err)
			}
			continue
		}
		handled[meta.Name] = true
		if res.Newer || force {
			fmt.Printf("[update] %s updated to %s. Restart gleann (or the plugin) to use it.\n", meta.Name, res.Latest)
		}
	}
	return handled
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown version"
	}
	return v
}
