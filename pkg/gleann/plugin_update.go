package gleann

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PluginUpdate describes the outcome of checking or updating one plugin.
type PluginUpdate struct {
	Name      string
	Installed string // version reported by the installed binary ("" if unknown)
	Latest    string // tag of the latest release
	Newer     bool   // Latest is higher than Installed (or Installed unknown)
	Verified  bool   // the downloaded archive matched a published checksum
}

// pluginBinary returns the installed plugin binary path: the one the plugin
// registered in plugins.json (`--install` records where it runs from), else the
// copy under ~/.gleann/plugins.
func pluginBinary(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if reg, err := LoadPlugins(); err == nil && reg != nil {
		for _, p := range reg.Plugins {
			if p.Name == name && len(p.Command) > 0 {
				if bin := expandHome(p.Command[0], home); fileExists(bin) {
					return bin, nil
				}
			}
		}
	}
	p := filepath.Join(home, ".gleann", "plugins", name, name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("plugin %s is not installed as a binary (%s)", name, p)
	}
	return p, nil
}

// installedPluginVersion runs `<binary> --version`; it returns "" when the
// binary does not report a version.
func installedPluginVersion(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	if v, ok := ParseVersion(string(out)); ok {
		return fmt.Sprintf("v%d.%d.%d", v[0], v[1], v[2])
	}
	return ""
}

// CheckPluginUpdate compares the installed binary with the latest release.
func CheckPluginUpdate(ctx context.Context, meta PluginMeta) (PluginUpdate, *Release, error) {
	res := PluginUpdate{Name: meta.Name}
	binary, err := pluginBinary(meta.Name)
	if err != nil {
		return res, nil, err
	}
	owner, repo := parseGitHubURL(meta.RepoURL)
	if owner == "" || repo == "" {
		return res, nil, fmt.Errorf("cannot parse repository URL %q", meta.RepoURL)
	}
	rel, err := FetchLatestRelease(ctx, owner, repo)
	if err != nil {
		return res, nil, err
	}
	res.Installed = installedPluginVersion(binary)
	res.Latest = rel.Tag
	if res.Installed == "" {
		res.Newer = true // cannot tell: an old build without --version
	} else {
		res.Newer, _ = IsNewer(res.Installed, rel.Tag)
	}
	return res, rel, nil
}

// UpdatePlugin installs the latest release binary of a plugin when it is newer
// than the installed one (or force is set), re-registers it and, for the docs
// plugin, refreshes its OCR runtime and models. A running plugin keeps serving
// the old build until gleann restarts it.
func UpdatePlugin(ctx context.Context, meta PluginMeta, force bool, progress chan<- string) (PluginUpdate, error) {
	res, rel, err := CheckPluginUpdate(ctx, meta)
	if err != nil {
		return res, err
	}
	if !res.Newer && !force {
		progress <- fmt.Sprintf("✓ %s is up to date (%s)", meta.Name, res.Installed)
		return res, nil
	}
	binary, _ := pluginBinary(meta.Name)
	_, repo := parseGitHubURL(meta.RepoURL)

	names := make([]string, len(rel.Assets))
	for i, a := range rel.Assets {
		names[i] = a.Name
	}
	idx, err := selectReleaseAsset(names, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return res, err
	}
	progress <- fmt.Sprintf("📥 Downloading %s (%s → %s)...", rel.Assets[idx].Name, orUnknown(res.Installed), rel.Tag)
	// Resolve the symlinks of the plugin directory so the new binary lands
	// beside the real file.
	if real, err := filepath.EvalSymlinks(binary); err == nil {
		binary = real
	}
	newPath, verified, err := DownloadReleaseBinary(ctx, rel, idx, repo, binary)
	if err != nil {
		return res, err
	}
	res.Verified = verified
	if err := ReplaceBinary(binary, newPath); err != nil {
		_ = os.Remove(newPath)
		return res, err
	}
	progress <- "✓ Binary replaced"
	reg := exec.Command(binary, "--install")
	reg.Dir = filepath.Dir(binary)
	if _, err := reg.CombinedOutput(); err == nil {
		progress <- "✓ Plugin registered in plugins.json"
	}
	if strings.Contains(meta.Name, "docs") {
		prepareDocsOCR(binary, progress)
	}
	return res, nil
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func expandHome(p, home string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[1:])
	}
	return p
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
