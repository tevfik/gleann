package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestTruncateOnRune(t *testing.T) {
	s := "aé€😀z" // 1 + 2 + 3 + 4 + 1 bytes
	for max := 1; max <= len(s)+2; max++ {
		got := truncateOnRune(s, max)
		if !utf8.ValidString(got) {
			t.Errorf("max %d: invalid UTF-8 %q", max, got)
		}
		if len(got) > max && max < len(s) {
			t.Errorf("max %d: got %d bytes", max, len(got))
		}
		if !strings.HasPrefix(s, got) {
			t.Errorf("max %d: %q is not a prefix", max, got)
		}
	}
	if got := truncateOnRune(s, 5); got != "aé" {
		t.Errorf("cut inside the euro sign: got %q, want %q", got, "aé")
	}
	if truncateOnRune(s, 0) != s {
		t.Error("max 0 means no limit")
	}
}

func TestMemoryContextMaxBytesKeepsCharacters(t *testing.T) {
	e := newCLIEnv(t)
	wantExit(t, e.run("memory", "remember", strings.Repeat("çğış€", 40)), 0)
	for _, n := range []string{"7", "8", "9", "51", "100"} {
		r := e.run("memory", "context", "--scope", "all", "--max-bytes", n)
		wantExit(t, r, 0)
		out := strings.TrimSuffix(r.Out, "\n")
		if !utf8.ValidString(out) || len(out) > atoiT(t, n) {
			t.Errorf("--max-bytes %s: %d bytes, valid=%v", n, len(out), utf8.ValidString(out))
		}
	}
	wantExit(t, e.run("memory", "context", "--max-bytes", "x"), 2)
}

func atoiT(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestMemoryRememberRepoScopeMeansDetectedRepo(t *testing.T) {
	e := newCLIEnv(t)
	want := memory.DetectRepoScope()
	if want == "" || want == "repo" {
		t.Fatalf("unusable detected scope %q", want)
	}
	wantExit(t, e.run("memory", "remember", "scoped-note", "--scope", "repo"), 0)
	wantExit(t, e.run("memory", "remember", "global-note"), 0)
	r := e.run("memory", "list", "--json")
	wantExit(t, r, 0)
	var blocks []memory.Block
	if err := json.Unmarshal([]byte(r.Out), &blocks); err != nil {
		t.Fatalf("%v\n%s", err, r.Out)
	}
	got := map[string]string{}
	for _, b := range blocks {
		got[b.Content] = b.Scope
	}
	if got["scoped-note"] != want || got["global-note"] != "" {
		t.Errorf("scopes = %v, want scoped-note=%q and global-note empty", got, want)
	}
	r = e.run("memory", "list")
	wantExit(t, r, 0)
	mustContain(t, "memory list", r.Out, "[scope: "+want+"]", "[scope: global]")
}

func TestInstallUninstallRemovesBlankClaudeMD(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "CLAUDE.md")
	if err := installClaudeAt(p, dir, getClaudeMDSection("px")); err != nil {
		t.Fatal(err)
	}
	if err := removeMarkedSection(p, "<!-- gleann: Code Intelligence", claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md that held only gleann's section must be removed: %v", err)
	}
	// User content survives.
	if err := os.WriteFile(p, []byte("# Mine\nRULE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeAt(p, dir, getClaudeMDSection("px")); err != nil {
		t.Fatal(err)
	}
	if err := removeMarkedSection(p, "<!-- gleann: Code Intelligence", claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "# Mine\nRULE\n" {
		t.Errorf("user content changed: %q", got)
	}
}

const l3UserSettings = `{
  "zeta": {
    "b": 1,
    "a": [
      {
        "y": 1,
        "x": 2
      }
    ]
  },
  "permissions": {
    "allow": [
      "Bash(ls 2>/dev/null)"
    ]
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "cat a >/dev/null 2>&1 && echo \"<done>\""
          }
        ]
      }
    ]
  },
  "alpha": "a&b"
}
`

func TestPatchClaudeSettingsKeepsOrderAndEscaping(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(l3UserSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := patchClaudeSettings(p); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if strings.Contains(got, `\u003e`) || strings.Contains(got, `\u0026`) || strings.Contains(got, `\u003c`) {
		t.Errorf("HTML escaping in output:\n%s", got)
	}
	last := -1
	for _, k := range []string{`"zeta"`, `"permissions"`, `"hooks"`, `"alpha"`} {
		i := strings.Index(got, k)
		if i < last {
			t.Errorf("top-level key order changed at %s:\n%s", k, got)
		}
		last = i
	}
	if strings.Index(got, `"y": 1`) > strings.Index(got, `"x": 2`) || strings.Index(got, `"b": 1`) > strings.Index(got, `"a": [`) {
		t.Errorf("nested key order changed:\n%s", got)
	}
	// Uninstall brings back the user's file byte for byte.
	home := t.TempDir()
	hp := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(hp), 0o755)
	_ = os.WriteFile(hp, []byte(got), 0o600)
	if err := uninstallClaudeAt(filepath.Join(home, "CLAUDE.md"), home); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, hp); after != l3UserSettings {
		t.Errorf("round trip changed the file:\n%s\nwant:\n%s", after, l3UserSettings)
	}
}

func TestPatchClaudeSettingsMigratesV1Hook(t *testing.T) {
	v1 := "if [ -f GRAPH_REPORT.md ]; then echo \"[gleann] GRAPH_REPORT.md found: read it for the repository overview.\"; fi; ctx=$(gleann memory context 2>/dev/null | head -c 2000); if [ -n \"$ctx\" ] && [ \"$ctx\" != \"(empty memory context)\" ]; then printf '[gleann] memory:\\n%s\\n' \"$ctx\"; fi " + gleannHookMarker + " v1"
	if !isGleannHookCommand(v1) {
		t.Fatal("a v1 hook must stay recognised as gleann-owned")
	}
	if strings.Contains(claudeHookScript, "head -c") || !strings.HasSuffix(claudeHookScript, " v2") {
		t.Errorf("current hook must use --max-bytes and marker v2: %s", claudeHookScript)
	}
	settings := map[string]interface{}{"hooks": map[string]interface{}{"SessionStart": []interface{}{
		map[string]interface{}{"hooks": []interface{}{map[string]interface{}{"type": "command", "command": v1, "timeout": float64(claudeHookTimeoutSeconds)}}},
	}}}
	data, _ := json.Marshal(settings)
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(p, data, 0o600)
	if err := patchClaudeSettings(p); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if strings.Contains(got, "v1") || strings.Count(got, gleannHookMarker) != 1 || !strings.Contains(got, "--max-bytes 2000") {
		t.Errorf("v1 hook not replaced by v2:\n%s", got)
	}
}

func TestUninstallThroughSymlinkKeepsLinkAndEditsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	old := followSymlinkFlag
	followSymlinkFlag = true
	t.Cleanup(func() { followSymlinkFlag = old })
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Skip(err)
	}
	const sentinel = "<!-- gleann: Code Intelligence"
	// Target with user content: the section goes, the content and the link stay.
	if err := os.WriteFile(target, []byte("# Mine\n"+sentinel+" & Search -->\nbody\n"+claudeSectionEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeMarkedSection(link, sentinel, claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink must survive: %v", err)
	}
	if got := readFile(t, target); got != "# Mine\n" {
		t.Errorf("target = %q", got)
	}
	// Target holding only the section: the target goes, never the link.
	if err := os.WriteFile(target, []byte(sentinel+" & Search -->\nbody\n"+claudeSectionEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeMarkedSection(link, sentinel, claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the symlink must survive: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("whitespace-only target should be removed: %v", err)
	}
}
