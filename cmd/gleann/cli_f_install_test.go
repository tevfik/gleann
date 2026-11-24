package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI tests of `gleann install` (platform integration) and `gleann agents`:
// listing and auto-detection, every platform's install/uninstall round trip,
// user-level versus project scope, and the failure modes of each writer.

// fMkdirs creates root and every directory under it.
func fMkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range append([]string{"."}, dirs...) {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// fReadFile returns the content of path, failing the test when it is absent.
func fReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// fBlockedPath returns a path whose parent is a regular file, so creating
// anything under it fails on every OS.
func fBlockedPath(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(f, "sub")
}

func TestCLIFInstallListAndDetection(t *testing.T) {
	e := newCLIEnv(t)
	proj := filepath.Join(e.Home, "proj")
	fMkdirs(t, proj, ".cursor")

	r := e.run("install", "--list", "--dir", proj)
	wantExit(t, r, 0)
	mustContain(t, "list", r.Out, "Supported platforms:", "cursor", "jetbrains")
	for _, line := range strings.Split(r.Out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		detected := strings.Contains(line, "detected")
		if (fields[0] == "cursor") != detected {
			t.Errorf("detection mark wrong on line %q", line)
		}
	}

	r = e.run("install", "--help")
	wantExit(t, r, 0)
	mustContain(t, "help", r.Out, "Usage: gleann install", "--follow-symlink", "amazonq")

	r = e.run("install", "--platform", "notepad", "--dir", proj)
	wantExit(t, r, 1)
	mustContain(t, "unknown platform", r.Err, `unknown platform "notepad"`, "gleann install --list")

	r = e.run("install", "--bogus")
	if r.Code == 0 {
		t.Fatalf("unknown flag accepted: %s", r.combined())
	}
}

func TestCLIFInstallAutoDetect(t *testing.T) {
	e := newCLIEnv(t)

	// Nothing to detect: the command explains how to pick a platform.
	bare := filepath.Join(e.Home, "bare")
	fMkdirs(t, bare)
	r := e.run("install", "--dir", bare)
	wantExit(t, r, 0)
	mustContain(t, "none detected", r.Out, "No supported AI platform detected", "--platform <name>")

	// Two project markers and one user-level marker are detected together.
	proj := filepath.Join(e.Home, "proj")
	fMkdirs(t, proj, ".codex", ".clinerules")
	fMkdirs(t, e.Home, ".continue")
	r = e.run("install", "--dir", proj)
	wantExit(t, r, 0)
	mustContain(t, "install", r.Out,
		"Target directory: "+proj,
		"Installing gleann for codex", "Installing gleann for cline", "Installing gleann for continue")
	mustNotContain(t, "install", r.Out, "Installing gleann for cursor", "No index found")
	agents := fReadFile(t, filepath.Join(proj, "AGENTS.md"))
	if strings.Count(agents, "gleann: Code Intelligence") != 1 {
		t.Errorf("AGENTS.md should hold one gleann section, got:\n%s", agents)
	}
	if _, err := os.Stat(filepath.Join(proj, ".codex", "hooks.json")); err != nil {
		t.Errorf("codex hooks.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".cline", "mcp.json")); err != nil {
		t.Errorf("cline mcp.json missing: %v", err)
	}

	r = e.run("install", "uninstall", "--dir", proj)
	wantExit(t, r, 0)
	mustContain(t, "uninstall", r.Out, "Uninstalling gleann from codex", "Uninstalling gleann from cline")
	if s := fReadFile(t, filepath.Join(proj, "AGENTS.md")); strings.Contains(s, "gleann: Code Intelligence") {
		t.Errorf("uninstall left the gleann section:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(proj, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Errorf("codex hooks.json not removed: %v", err)
	}
}

// Every platform installs into a fresh project and uninstalls cleanly.
func TestCLIFInstallEveryPlatformRoundTrip(t *testing.T) {
	e := newCLIEnv(t)
	for _, p := range platforms {
		t.Run(p.Name, func(t *testing.T) {
			proj := filepath.Join(e.Home, "proj-"+p.Name)
			fMkdirs(t, proj)
			r := e.run("install", p.Name, "--dir", proj)
			wantExit(t, r, 0)
			mustContain(t, "install "+p.Name, r.Out, "Installing gleann for "+p.Name, "✓ Done")
			mustNotContain(t, "install "+p.Name, r.Err, "error")

			// Installing twice is idempotent: no duplicate sections.
			r = e.run("install", "--platform", p.Name, "--dir", proj)
			wantExit(t, r, 0)
			mustNotContain(t, "reinstall "+p.Name, r.Err, "error")
			for _, f := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
				if b, err := os.ReadFile(filepath.Join(proj, f)); err == nil {
					if n := strings.Count(string(b), "gleann knowledge graph") + strings.Count(string(b), "## gleann: Code Intelligence"); n > 1 {
						t.Errorf("%s has %d gleann sections after reinstall", f, n)
					}
				}
			}

			r = e.run("install", "uninstall", "--platform", p.Name, "--dir", proj)
			wantExit(t, r, 0)
			mustContain(t, "uninstall "+p.Name, r.Out, "Uninstalling gleann from "+p.Name, "✓ Done")
			for _, f := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
				if b, err := os.ReadFile(filepath.Join(proj, f)); err == nil && strings.Contains(string(b), "<!-- gleann") {
					t.Errorf("%s still has the gleann section after uninstall:\n%s", f, b)
				}
			}
		})
	}
}

// claude and gemini default to the user-level files; --project keeps them in
// the project.
func TestCLIFInstallUserScope(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("install", "claude")
	wantExit(t, r, 0)
	userClaude := filepath.Join(e.Home, ".claude", "CLAUDE.md")
	mustContain(t, "user CLAUDE.md", fReadFile(t, userClaude), "gleann knowledge graph")
	settings := fReadFile(t, filepath.Join(e.Home, ".claude", "settings.json"))
	mustContain(t, "settings hook", settings, "PreToolUse", "GRAPH_REPORT.md")

	r = e.run("install", "gemini")
	wantExit(t, r, 0)
	mustContain(t, "user GEMINI.md", fReadFile(t, filepath.Join(e.Home, ".gemini", "GEMINI.md")), "<!-- gleann -->")

	proj := filepath.Join(e.Home, "proj")
	fMkdirs(t, proj)
	t.Chdir(proj)
	r = e.run("install", "claude", "--project")
	wantExit(t, r, 0)
	mustContain(t, "project CLAUDE.md", fReadFile(t, filepath.Join(proj, "CLAUDE.md")), "gleann knowledge graph")

	r = e.run("install", "uninstall", "claude")
	wantExit(t, r, 0)
	if s := fReadFile(t, userClaude); strings.Contains(s, "gleann") {
		t.Errorf("user CLAUDE.md still mentions gleann:\n%s", s)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(fReadFile(t, filepath.Join(e.Home, ".claude", "settings.json"))), &st); err != nil {
		t.Fatal(err)
	}
	hooks, _ := st["hooks"].(map[string]any)
	if pre, _ := hooks["PreToolUse"].([]any); len(pre) != 0 {
		t.Errorf("gleann hook not removed: %v", pre)
	}
	// The project file is untouched by the user-level uninstall.
	mustContain(t, "project CLAUDE.md kept", fReadFile(t, filepath.Join(proj, "CLAUDE.md")), "gleann knowledge graph")

	r = e.run("install", "uninstall", "gemini")
	wantExit(t, r, 0)
	if s := fReadFile(t, filepath.Join(e.Home, ".gemini", "GEMINI.md")); strings.Contains(s, "<!-- gleann -->") {
		t.Errorf("user GEMINI.md still has the section:\n%s", s)
	}
}

// --index names the index the instructions refer to; an index's source
// directory becomes the target when no --dir is given.
func TestCLIFInstallIndexTargets(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	r := e.run("install", "aider", "--index", "demo")
	wantExit(t, r, 0)
	mustContain(t, "target", r.Out, `(index: "demo")`)
	mustContain(t, "AGENTS.md in source dir", fReadFile(t, filepath.Join(e.Docs, "AGENTS.md")), "demo")

	// GLEANN_INDEX names the index when no flag does.
	other := filepath.Join(e.Home, "other")
	fMkdirs(t, other)
	t.Setenv("GLEANN_INDEX", "fromenv")
	r = e.run("install", "amp", "--dir", other)
	wantExit(t, r, 0)
	mustContain(t, "env index", r.Out, `(index: "fromenv")`)
	mustContain(t, "env index AGENTS.md", fReadFile(t, filepath.Join(other, "AGENTS.md")), "fromenv")
	t.Setenv("GLEANN_INDEX", "")

	// A directory named like an index resolves to it.
	named := filepath.Join(e.Home, "Demo")
	fMkdirs(t, named)
	if got := resolveIndexNameForDir(named, ""); got != "demo" {
		t.Errorf("resolveIndexNameForDir(%s) = %q, want demo", named, got)
	}
	// A directory inside the source tree resolves to the index too.
	if got := resolveIndexNameForDir(filepath.Join(e.Docs, "auth"), ""); got != "demo" {
		t.Errorf("resolveIndexNameForDir(sub dir) = %q, want demo", got)
	}

	// An unknown index falls back to the working directory.
	t.Chdir(other)
	got, err := resolveTargetDirectory("", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(other); got != other && got != want {
		if top, gerr := getGitToplevel(); gerr != nil || got != top {
			t.Errorf("resolveTargetDirectory = %q, want %q", got, other)
		}
	}
}

// Each platform reports why it could not write its files.
func TestFPlatformInstallFailures(t *testing.T) {
	t.Setenv("GLEANN_INDEX", "x") // skip index discovery
	blocked := fBlockedPath(t)
	okHome := t.TempDir()
	for _, p := range platforms {
		t.Run(p.Name, func(t *testing.T) {
			// A project directory under a file breaks the project writers; a
			// home under a file breaks the user-level ones.
			home := okHome
			switch p.Name {
			case "copilot":
				home = blocked
			}
			err := p.Install(blocked, home)
			if err == nil {
				t.Fatalf("%s: Install under a file succeeded", p.Name)
			}
			if p.InstallUser != nil {
				if err := p.InstallUser(blocked); err == nil {
					t.Errorf("%s: InstallUser under a file succeeded", p.Name)
				}
			}
		})
	}

	// claw writes AGENTS.md into the project first, then the skill in home.
	proj := t.TempDir()
	if err := installClaw(proj, blocked); err == nil || !strings.Contains(err.Error(), "creating skill dir") {
		t.Errorf("installClaw with a blocked home: %v", err)
	}
	// claude writes the CLAUDE.md, then fails on ~/.claude.
	if err := installClaude(proj, blocked); err == nil || !strings.Contains(err.Error(), "creating ~/.claude") {
		t.Errorf("installClaude with a blocked home: %v", err)
	}
	// A settings.json that is not JSON stops claude and gemini.
	badHome := t.TempDir()
	fMkdirs(t, badHome, ".claude")
	if err := os.WriteFile(filepath.Join(badHome, ".claude", "settings.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installClaude(t.TempDir(), badHome); err == nil || !strings.Contains(err.Error(), "patching ~/.claude/settings.json") {
		t.Errorf("installClaude with bad settings: %v", err)
	}
	gproj := t.TempDir()
	fMkdirs(t, gproj, ".gemini")
	if err := os.WriteFile(filepath.Join(gproj, ".gemini", "settings.json"), []byte("[1,"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installGemini(gproj, okHome); err == nil || !strings.Contains(err.Error(), "patching .gemini/settings.json") {
		t.Errorf("installGemini with bad settings: %v", err)
	}
	// opencode: a broken opencode.json in the project is an error.
	oproj := t.TempDir()
	if err := os.WriteFile(filepath.Join(oproj, "opencode.json"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installOpenCode(oproj, okHome); err == nil || !strings.Contains(err.Error(), "patching opencode.json") {
		t.Errorf("installOpenCode with bad opencode.json: %v", err)
	}
	// A file where a directory must be created, or a directory where a file
	// must be written: each writer names the step that failed.
	for _, tc := range []struct {
		name    string
		install func(dir, home string) error
		occupy  string
		asDir   bool // occupy with a directory (else with a regular file)
		want    string
	}{
		{"cursor rules", installCursor, filepath.Join(".cursor", "rules", "gleann.mdc"), true, "writing rules file"},
		{"codex dir", installCodex, ".codex", false, "creating .codex"},
		{"windsurf", installWindsurf, filepath.Join(".windsurf", "rules", "gleann.md"), true, "writing rules"},
		{"kiro", installKiro, filepath.Join(".kiro", "rules", "gleann.md"), true, "writing rules"},
		{"cline dir", installCline, ".cline", false, "creating .cline"},
		{"jetbrains dir", installJetBrains, ".junie", false, "creating .junie"},
		{"jetbrains guidelines", installJetBrains, filepath.Join(".junie", "guidelines.md"), true, "writing guidelines.md"},
		{"opencode plugin dir", installOpenCode, ".opencode", false, "creating plugin dir"},
		{"opencode plugin", installOpenCode, filepath.Join(".opencode", "plugins", "gleann.js"), true, "writing plugin"},
		{"gemini md", installGemini, "GEMINI.md", true, "writing GEMINI.md"},
		{"claude md", installClaude, "CLAUDE.md", true, "writing CLAUDE.md"},
		{"claw agents", installClaw, "AGENTS.md", true, "writing AGENTS.md"},
		{"agents", platformByName("agents").Install, "AGENTS.md", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.asDir {
				fMkdirs(t, dir, tc.occupy)
			} else {
				fMkdirs(t, dir, filepath.Dir(tc.occupy))
				if err := os.WriteFile(filepath.Join(dir, tc.occupy), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := tc.install(dir, t.TempDir())
			if err == nil {
				t.Fatalf("install succeeded with %s occupied", tc.occupy)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// Platforms detect their user-level markers in home.
func TestFPlatformDetectHomeMarkers(t *testing.T) {
	for _, tc := range []struct {
		platform string
		dirMark  string
		homeMark string
	}{
		{"opencode", "opencode.json", ""},
		{"opencode", "", filepath.Join(".config", "opencode")},
		{"opencode", "", ".opencode"},
		{"cline", ".cline", ""},
		{"amazonq", "", ".amazonq"},
		{"amazonq", ".amazonq", ""},
		{"continue", "", ".continue"},
		{"zed", "", filepath.Join(".config", "zed")},
		{"neovim", "", filepath.Join(".config", "nvim")},
		{"jetbrains", ".idea", ""},
		{"kiro", ".kiro", ""},
		{"amp", ".amp", ""},
		{"windsurf", ".windsurf", ""},
		{"aider", ".aider", ""},
		{"gemini", ".gemini", ""},
	} {
		dir, home := t.TempDir(), t.TempDir()
		p := platformByName(tc.platform)
		if p.Detect(dir, home) {
			t.Fatalf("%s detected in empty dirs", tc.platform)
		}
		if tc.dirMark != "" {
			fMkdirs(t, dir, tc.dirMark)
		}
		if tc.homeMark != "" {
			fMkdirs(t, home, tc.homeMark)
		}
		if !p.Detect(dir, home) {
			t.Errorf("%s not detected with dir mark %q, home mark %q", tc.platform, tc.dirMark, tc.homeMark)
		}
	}
}

// Symlinked targets are left alone unless --follow-symlink is given.
func TestCLIFInstallSymlinkGuard(t *testing.T) {
	e := newCLIEnv(t)
	proj := filepath.Join(e.Home, "proj")
	fMkdirs(t, proj)
	real := filepath.Join(e.Home, "shared-agents.md")
	if err := os.WriteFile(real, []byte("# Shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(proj, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := e.run("install", "aider", "--dir", proj)
	wantExit(t, r, 0)
	mustContain(t, "skip", r.Out, "Skipped", "--follow-symlink")
	if s := fReadFile(t, real); strings.Contains(s, "gleann") {
		t.Errorf("symlink target modified without --follow-symlink:\n%s", s)
	}
	r = e.run("install", "uninstall", "aider", "--dir", proj)
	wantExit(t, r, 0)
	mustContain(t, "skip uninstall", r.Out, "Skipped")

	r = e.run("install", "aider", "--dir", proj, "--follow-symlink")
	wantExit(t, r, 0)
	mustContain(t, "followed", fReadFile(t, real), "# Shared", "gleann: Code Intelligence")
}

// patchOpenCodeJSON repairs an existing registration: a command that points
// into an ephemeral runtime directory is replaced and --clean-names added,
// and an unrelated legacy plugin survives the cleanup.
func TestFPatchOpenCodeJSONHealsStaleEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	in := `{"plugins": {"gleann": {}, "other": {"x": 1}},
	        "mcp": {"servers": {"gleann": {"type": "local", "command": ["/home/u/.gleann/runtime/gleann", "mcp"]}}}}`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := patchOpenCodeJSON(path); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(fReadFile(t, path)), &cfg); err != nil {
		t.Fatal(err)
	}
	plugins, _ := cfg["plugins"].(map[string]any)
	if _, ok := plugins["other"]; !ok || plugins["gleann"] != nil {
		t.Errorf("legacy plugin cleanup wrong: %v", plugins)
	}
	cmd := cfg["mcp"].(map[string]any)["servers"].(map[string]any)["gleann"].(map[string]any)["command"].([]any)
	if s, _ := cmd[0].(string); strings.Contains(filepath.ToSlash(s), ".gleann/runtime") {
		t.Errorf("stale runtime path kept: %v", cmd)
	}
	if cmd[len(cmd)-1] != "--clean-names" {
		t.Errorf("--clean-names not appended: %v", cmd)
	}

	// Uninstall keeps other MCP servers.
	dir := t.TempDir()
	oc := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(oc, []byte(`{"mcp": {"servers": {"gleann": {}, "keep": {}}, "other": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := uninstallOpenCode(dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	s := fReadFile(t, oc)
	mustContain(t, "kept servers", s, `"keep"`, `"other"`)
	mustNotContain(t, "removed gleann", s, `"gleann"`)
}

func TestFResolveInstalledGleannBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	_, sysErr := os.Stat("/usr/local/bin/gleann")

	got := resolveInstalledGleannBin()
	if sysErr != nil && got == filepath.Join(home, ".local", "bin", "gleann") {
		t.Errorf("returned a user binary that does not exist: %s", got)
	}

	userBin := filepath.Join(home, ".local", "bin", "gleann")
	fMkdirs(t, home, filepath.Join(".local", "bin"))
	if err := os.WriteFile(userBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = resolveInstalledGleannBin()
	if sysErr == nil {
		if got != "/usr/local/bin/gleann" {
			t.Errorf("got %q, want the system binary", got)
		}
	} else if got != userBin {
		t.Errorf("got %q, want %q", got, userBin)
	}
}

func TestCLIFAgents(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("agents", "--help")
	wantExit(t, r, 0)
	mustContain(t, "help", r.Out, "gleann agents", "--force")

	// With no index the instructions use the placeholder; in the working
	// directory by default.
	r = e.run("agents")
	wantExit(t, r, 0)
	cwd, _ := os.Getwd()
	mustContain(t, "default dir", r.Out, "AGENTS.md generated/updated in "+filepath.Join(cwd, "AGENTS.md"))

	// GLEANN_INDEX names the index.
	proj := filepath.Join(e.Home, "proj")
	fMkdirs(t, proj)
	t.Setenv("GLEANN_INDEX", "envidx")
	r = e.run("agents", "--stdout")
	wantExit(t, r, 0)
	mustContain(t, "env index", r.Out, "envidx")
	t.Setenv("GLEANN_INDEX", "")

	// With exactly one index, it is used.
	e.sampleDocs()
	e.build("solo")
	r = e.run("agents", "--dir", proj)
	wantExit(t, r, 0)
	mustContain(t, "single index", fReadFile(t, filepath.Join(proj, "AGENTS.md")), "solo")

	// A second run does not duplicate the section; --force rewrites it.
	r = e.run("agents", "--dir", proj, "--index", "other")
	wantExit(t, r, 0)
	if s := fReadFile(t, filepath.Join(proj, "AGENTS.md")); strings.Contains(s, "other") {
		t.Errorf("existing section rewritten without --force:\n%s", s)
	}
	r = e.run("agents", "--dir", proj, "--index", "other", "--force")
	wantExit(t, r, 0)
	mustContain(t, "force", r.Out, "overwritten")
	s := fReadFile(t, filepath.Join(proj, "AGENTS.md"))
	mustContain(t, "forced", s, "other")
	mustNotContain(t, "forced", s, "solo")

	// A directory named like an index wins over the single-index fallback.
	e.build("proj")
	named := filepath.Join(e.Home, "PROJ")
	if !strings.EqualFold(filepath.Base(named), "proj") {
		t.Fatal("bad fixture")
	}
	fMkdirs(t, named)
	r = e.run("agents", "--docs", named, "--stdout")
	wantExit(t, r, 0)
	mustContain(t, "name match", r.Out, "proj")

	// Unwritable targets fail with the path.
	blocked := fBlockedPath(t)
	r = e.run("agents", "--dir", blocked)
	wantExit(t, r, 1)
	mustContain(t, "write error", r.Err, "error updating")
	r = e.run("agents", "--dir", blocked, "--force")
	wantExit(t, r, 1)
	mustContain(t, "force write error", r.Err, "error writing")
}
