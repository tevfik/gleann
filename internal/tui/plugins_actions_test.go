package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tevfik/gleann/pkg/gleann"
)

func actionTestModel(statuses ...pluginStatus) PluginModel {
	plugins := make([]gleann.PluginMeta, len(statuses))
	for i := range plugins {
		plugins[i] = gleann.PluginMeta{Name: "plug-" + string(rune('a'+i)), HasSettings: true, SettingsCmd: []string{"plug-tool", "tui"}}
	}
	return PluginModel{plugins: plugins, statuses: statuses, state: psDetail, width: 80, height: 24, registry: &gleann.PluginRegistry{}}
}

func TestListenForProgress(t *testing.T) {
	ch := make(chan string, 4)
	ch <- "cloning"
	ch <- "SUCCESS: installed at /x"
	ch <- "ERROR: boom"
	cmd := listenForProgress("p", ch)

	prog, ok := cmd().(pluginInstallProgressMsg)
	if !ok || prog.plugin != "p" || prog.message != "cloning" || prog.continueCmd == nil {
		t.Fatalf("progress line must carry a continuation: %+v", prog)
	}
	done, ok := prog.continueCmd().(pluginActionMsg)
	if !ok || done.err != nil || done.output != "installed at /x" || done.action != "install" {
		t.Fatalf("success: %+v", done)
	}
	failed := listenForProgress("p", ch)().(pluginActionMsg)
	if failed.err == nil || failed.err.Error() != "boom" {
		t.Errorf("error: %+v", failed)
	}
	close(ch)
	closed := listenForProgress("p", ch)().(pluginActionMsg)
	if closed.err == nil || !strings.Contains(closed.err.Error(), "interrupted") {
		t.Errorf("a channel closed without a result is an interrupted install: %+v", closed)
	}
}

func TestPluginUpdateDetailActions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Run("uninstall only when installed", func(t *testing.T) {
		m := actionTestModel(statusNotInstalled)
		got, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'u'})
		if cmd != nil || got.(PluginModel).state != psDetail {
			t.Error("u on a plugin that is not installed must do nothing")
		}
		m = actionTestModel(statusInstalled)
		got, cmd = m.updateDetail(tea.KeyPressMsg{Code: 'u'})
		pm := got.(PluginModel)
		if pm.state != psAction || !strings.Contains(pm.actionMsg, "Uninstalling plug-a") || cmd == nil {
			t.Fatalf("state=%v msg=%q cmd=%v", pm.state, pm.actionMsg, cmd)
		}
		msg := cmd().(pluginActionMsg)
		if msg.action != "uninstall" || msg.plugin != "plug-a" || msg.err != nil {
			t.Errorf("uninstall result: %+v", msg)
		}
	})
	t.Run("uninstall surfaces an invalid name", func(t *testing.T) {
		m := actionTestModel(statusInstalled)
		m.plugins[0].Name = "../escape"
		_, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'u'})
		if msg := cmd().(pluginActionMsg); msg.err == nil {
			t.Error("a path-traversal plugin name must be rejected")
		}
	})
	t.Run("install only when missing", func(t *testing.T) {
		m := actionTestModel(statusInstalled)
		if _, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'i'}); cmd != nil {
			t.Error("i on an installed plugin must do nothing")
		}
	})
	t.Run("configure needs an installed plugin with settings", func(t *testing.T) {
		m := actionTestModel(statusNotInstalled)
		if _, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'c'}); cmd != nil {
			t.Error("c on a missing plugin must do nothing")
		}
		m = actionTestModel(statusInstalled)
		m.plugins[0].HasSettings = false
		if _, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'c'}); cmd != nil {
			t.Error("c without settings must do nothing")
		}
	})
	t.Run("markitdown only when required and missing", func(t *testing.T) {
		m := actionTestModel(statusInstalled)
		if _, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'm'}); cmd != nil {
			t.Error("m must be inert when the plugin does not need markitdown")
		}
		m.plugins[0].RequiresMarkitdown = true
		m.markitdown.available = true
		if _, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'm'}); cmd != nil {
			t.Error("m must be inert when markitdown is present")
		}
		m.markitdown.available = false
		got, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'm'})
		if cmd == nil || got.(PluginModel).state != psAction || !strings.Contains(got.(PluginModel).actionMsg, "markitdown") {
			t.Errorf("m should start the install: %+v", got.(PluginModel).actionMsg)
		}
	})
}

func TestPluginStartInstallReportsFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := actionTestModel(statusNotInstalled)
	// A repository that cannot exist: the clone fails locally, with no network.
	m.plugins[0].RepoURL = filepath.Join(home, "no-such-repo")
	got, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'i'})
	pm := got.(PluginModel)
	if pm.state != psAction || !strings.Contains(pm.actionMsg, "Installing plug-a") || cmd == nil {
		t.Fatalf("state=%v msg=%q", pm.state, pm.actionMsg)
	}
	var final pluginActionMsg
	for i := 0; i < 50; i++ {
		switch msg := cmd().(type) {
		case pluginInstallProgressMsg:
			cmd = msg.continueCmd
			continue
		case pluginActionMsg:
			final = msg
		}
		break
	}
	if final.err == nil || final.plugin != "plug-a" || final.action != "install" {
		t.Errorf("an install from a missing repository must fail: %+v", final)
	}
}

func TestPluginStartConfigure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // plug-tool is nowhere on PATH

	m := actionTestModel(statusInstalled)
	m.plugins[0].SettingsCmd = nil
	if _, cmd := m.startConfigure(); cmd != nil {
		t.Error("no settings command: nothing to launch")
	}

	m = actionTestModel(statusInstalled)
	_, cmd := m.startConfigure()
	msg, ok := cmd().(pluginActionMsg)
	if !ok || msg.err == nil || !strings.Contains(msg.err.Error(), `"plug-tool" not found`) || msg.action != "configure" {
		t.Fatalf("missing binary must be reported: %+v", msg)
	}

	// With the binary in the plugin directory a process command is returned.
	dir := filepath.Join(home, ".gleann", "plugins", "plug-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plug-tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, cmd := m.startConfigure(); cmd == nil {
		t.Error("an installed settings binary must produce an exec command")
	}
	// And through the key handler.
	got, cmd := m.updateDetail(tea.KeyPressMsg{Code: 'c'})
	if cmd == nil || got.(PluginModel).state != psDetail {
		t.Error("c must launch the settings TUI")
	}
}

func TestResolveBinaryLayouts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("PATH", t.TempDir())
	info := gleann.PluginMeta{Name: "plug-a", SettingsCmd: []string{"plug-tool", "tui"}}
	// Executables carry .exe on Windows; exec.LookPath and resolveBinary add it.
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}

	if got := resolveBinary(info, nil); got != "" {
		t.Errorf("nothing installed: %q", got)
	}

	// PATH wins over the plugin directory.
	pathDir := t.TempDir()
	onPath := filepath.Join(pathDir, "plug-tool"+exe)
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if got := resolveBinary(info, nil); got != onPath {
		t.Errorf("PATH: %q", got)
	}
	t.Setenv("PATH", t.TempDir())

	// A registered command with another base name is ignored; a directory is not a binary.
	reg := &gleann.Plugin{Name: "plug-a", Command: []string{pathDir}}
	if got := resolveBinary(info, reg); got != "" {
		t.Errorf("directory registered as command: %q", got)
	}

	// Symlinked plugin entry: pluginDir -> <repo>/plug-a (the binary); the
	// settings binary sits beside it in the repo root.
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "plug-a"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "plug-tool"+exe), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(home, ".gleann", "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "plug-a"), filepath.Join(plugins, "plug-a")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// resolveBinary follows the symlink, so compare with the resolved repo
	// directory (macOS: /var is a symlink to /private/var).
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveBinary(info, nil); got != filepath.Join(realRepo, "plug-tool"+exe) {
		t.Errorf("repo-root layout: %q", got)
	}
}

func TestRegisteredHonoursAliases(t *testing.T) {
	reg := &gleann.PluginRegistry{Plugins: []gleann.Plugin{{Name: "gleann-sound"}, {Name: "other"}}}
	info := gleann.PluginMeta{Name: "gleann-plugin-sound"}
	p := registered(reg, info)
	if p == nil || p.Name != "gleann-sound" {
		t.Errorf("legacy short name must match the renamed plugin: %+v", p)
	}
	if registered(reg, gleann.PluginMeta{Name: "gleann-plugin-docs"}) != nil {
		t.Error("unrelated plugin must not match")
	}
	if registered(nil, info) != nil {
		t.Error("nil registry")
	}
}

func TestPluginCheckStatusByDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	m := &PluginModel{registry: &gleann.PluginRegistry{}}
	info := gleann.PluginMeta{Name: "gleann-plugin-vision"}
	if got := m.checkPluginStatus(info); got != statusNotInstalled {
		t.Errorf("empty home: %v", got)
	}
	// An installed directory under its legacy name counts.
	if err := os.MkdirAll(filepath.Join(home, ".gleann", "plugins", "gleann-vision"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := m.checkPluginStatus(info); got != statusInstalled {
		t.Errorf("legacy directory: %v", got)
	}
	m.registry = &gleann.PluginRegistry{Plugins: []gleann.Plugin{{Name: "gleann-plugin-vision"}}}
	if got := m.checkPluginStatus(info); got != statusInstalled {
		t.Errorf("registered without URL is installed, not running: %v", got)
	}
	m.registry = nil
	if got := m.checkPluginStatus(info); got != statusNotInstalled {
		t.Errorf("no registry: %v", got)
	}
}
