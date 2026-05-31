package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// cmdPlugin handles `gleann plugin <subcommand>` for managing installed
// plugins. Installation/uninstallation still lives in the TUI; this CLI
// surface exists so users can script status checks and version updates.
//
// Subcommands:
//
//	list                       Show registered plugins with version + status
//	update [name]              git pull + (rebuild) for a single plugin
//	                           or all repo-installed plugins when omitted
//	info <name>                Fetch /info from the plugin's HTTP endpoint
//
// Plugins installed via `gleann tui` live under
//
//	~/.gleann/plugins/_repos/<repo-name>
//
// and `update` runs `git fetch --tags && git reset --hard origin/<branch>`
// followed by the plugin-specific build/install command discovered from
// the registry (the Command field's first entry, typically the binary).
func cmdPlugin(args []string) {
	if len(args) == 0 {
		pluginUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list", "ls":
		pluginList()
	case "update", "upgrade":
		pluginUpdate(args[1:])
	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: gleann plugin info <name>")
			os.Exit(1)
		}
		pluginInfo(args[1])
	case "help", "-h", "--help":
		pluginUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin subcommand: %s\n\n", args[0])
		pluginUsage()
		os.Exit(1)
	}
}

func pluginUsage() {
	fmt.Println(`gleann plugin — manage installed plugins

Usage:
  gleann plugin list                Show registered plugins + live status
  gleann plugin info <name>         Fetch live /info from a plugin
  gleann plugin update [name]       git pull + rebuild a plugin (all if omitted)

Plugins are auto-discovered from ~/.gleann/plugins.json. Audio and vision
plugins are auto-wired into the multimodal pipeline by capability.`)
}

func pluginList() {
	mgr, err := gleann.NewPluginManager()
	if err != nil || mgr == nil {
		fmt.Fprintf(os.Stderr, "could not load plugins: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()
	if len(mgr.Registry.Plugins) == 0 {
		fmt.Println("No plugins installed. Run `gleann tui` and open the Plugins panel.")
		return
	}
	rows := make([]string, 0, len(mgr.Registry.Plugins))
	for i := range mgr.Registry.Plugins {
		p := &mgr.Registry.Plugins[i]
		status := "stopped"
		schema := p.SchemaVersion
		ver := p.Version
		if h, err := mgr.PingPlugin(p); err == nil && h != nil {
			if h.Ready {
				status = "ready"
			} else {
				status = "loading"
			}
			if h.SchemaVersion > 0 {
				schema = h.SchemaVersion
			}
			if h.Version != "" {
				ver = h.Version
			}
		}
		if ver == "" {
			ver = "?"
		}
		schemaStr := "?"
		if schema > 0 {
			schemaStr = fmt.Sprintf("v%d", schema)
		}
		caps := strings.Join(p.Capabilities, ",")
		rows = append(rows, fmt.Sprintf("%-26s  %-8s  schema=%-3s  version=%-10s  %s",
			p.Name, status, schemaStr, ver, caps))
	}
	sort.Strings(rows)
	fmt.Println("NAME                          STATUS    SCHEMA      VERSION        CAPABILITIES")
	for _, r := range rows {
		fmt.Println(r)
	}
}

func pluginInfo(name string) {
	mgr, err := gleann.NewPluginManager()
	if err != nil || mgr == nil {
		fmt.Fprintf(os.Stderr, "could not load plugins: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()
	var target *gleann.Plugin
	for i := range mgr.Registry.Plugins {
		if mgr.Registry.Plugins[i].Name == name {
			target = &mgr.Registry.Plugins[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "plugin %q not registered\n", name)
		os.Exit(1)
	}
	info, err := mgr.FetchPluginInfo(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not reach %s: %v\n", name, err)
		fmt.Fprintln(os.Stderr, "(start it manually or run `gleann build` once to auto-start)")
		os.Exit(1)
	}
	fmt.Printf("Name           %s\n", info.Name)
	fmt.Printf("Version        %s\n", info.Version)
	fmt.Printf("SchemaVersion  %d\n", info.SchemaVersion)
	fmt.Printf("Capabilities   %s\n", strings.Join(info.Capabilities, ", "))
	fmt.Printf("Extensions     %s\n", strings.Join(info.Extensions, ", "))
}

// pluginUpdate runs `git pull --tags --rebase` in each repo-installed
// plugin's source directory. Binary-only installs (no _repos/ dir) are
// skipped with a notice.
func pluginUpdate(names []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reposDir := filepath.Join(home, ".gleann", "plugins", "_repos")
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no plugin repos found under %s\n", reposDir)
		os.Exit(1)
	}

	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}

	updated, skipped := 0, 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if len(wanted) > 0 && !wanted[e.Name()] {
			// Allow matching by short alias (gleann-sound vs gleann-plugin-sound).
			alt := strings.TrimPrefix(e.Name(), "gleann-plugin-")
			if !wanted["gleann-plugin-"+alt] && !wanted["gleann-"+alt] {
				continue
			}
		}
		repoPath := filepath.Join(reposDir, e.Name())
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
			fmt.Printf("[skip]   %s — not a git checkout\n", e.Name())
			skipped++
			continue
		}
		fmt.Printf("[update] %s\n", e.Name())
		if err := runGit(repoPath, "fetch", "--tags", "--prune"); err != nil {
			fmt.Fprintf(os.Stderr, "         fetch failed: %v\n", err)
			skipped++
			continue
		}
		// Find latest tag (if any) and check out; otherwise fast-forward to HEAD.
		tag, _ := gitOutput(repoPath, "describe", "--tags", "--abbrev=0")
		tag = strings.TrimSpace(tag)
		if tag != "" {
			if err := runGit(repoPath, "checkout", tag); err != nil {
				fmt.Fprintf(os.Stderr, "         checkout %s failed: %v\n", tag, err)
				continue
			}
			fmt.Printf("         checked out tag %s\n", tag)
		} else {
			if err := runGit(repoPath, "pull", "--ff-only"); err != nil {
				fmt.Fprintf(os.Stderr, "         pull failed: %v\n", err)
				continue
			}
		}
		updated++
	}

	fmt.Printf("\nDone. %d updated, %d skipped.\n", updated, skipped)
	fmt.Println("Tip: re-run `gleann tui` and reinstall the plugin if its binary needs rebuilding.")
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
