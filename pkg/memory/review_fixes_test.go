package memory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// R1: a symlinked subdirectory inside the export dir must not let a prune
// delete a file outside it.
func TestExportPruneNeverFollowsSymlinks(t *testing.T) {
	s, _ := seedExportBlocks(t)
	dir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim-deadbeef.md")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "summaries")); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName),
		[]byte(`{"version":1,"files":{"all":["summaries/victim-deadbeef.md"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// MkdirAll tolerates the symlinked dir; the run must still not delete.
	_, _ = s.ExportWithOptions(dir, ExportOptions{})
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the export dir was deleted: %v", err)
	}
}

// R1: a manifest entry that is not a generated file name is never deleted.
func TestExportPruneIgnoresForeignManifestNames(t *testing.T) {
	s, _ := seedExportBlocks(t)
	dir := t.TempDir()
	foreign := filepath.Join(dir, "important.txt")
	notesMD := filepath.Join(dir, "notes.md")
	for _, f := range []string{foreign, notesMD} {
		if err := os.WriteFile(f, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName),
		[]byte(`{"version":1,"files":{"all":["important.txt","notes.md"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := s.ExportWithOptions(dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0", st.Pruned)
	}
	for _, f := range []string{foreign, notesMD} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s deleted: %v", f, err)
		}
	}
}

// R2: a symlinked path and the real path are one scope.
func TestDetectRepoScopeResolvesSymlinks(t *testing.T) {
	real := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	if a, b := DetectRepoScopeAt(real), DetectRepoScopeAt(link); a != b {
		t.Fatalf("real %q != symlink %q", a, b)
	}
}

// R3: importing a pre-v1.22 backup into a fresh store makes its bare-scope
// blocks visible from the matching directory, and only from it.
func TestImportRegistersLegacyAlias(t *testing.T) {
	src, err := OpenStore(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, b := range []Block{
		{ID: "bk-app", Tier: TierLong, Label: "fact", Content: "zz app fact", Scope: "app"},
		{ID: "bk-other", Tier: TierLong, Label: "fact", Content: "zz other fact", Scope: "other"},
	} {
		b := b
		if err := src.Add(&b); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if _, err := src.Export(dir); err != nil {
		t.Fatal(err)
	}

	dst, err := OpenStore(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(dst)
	const cur = "dir:app-1a2b3c4d"
	if _, err := dst.ImportWithOptions(dir, ImportOptions{CurrentScope: cur}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.ListScoped(cur, TierLong)
	if len(got) != 1 || got[0].ID != "bk-app" {
		t.Fatalf("ListScoped(%q) = %v, want only bk-app", cur, got)
	}
	// A different folder name is not aliased to "app".
	got, _ = m.ListScoped("dir:zzz-1a2b3c4d", TierLong)
	if len(got) != 0 {
		t.Fatalf("unrelated dir sees %v", got)
	}
	// The alias survives a reopen.
	path := dst.path
	dst.Close()
	re, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	got, _ = NewManager(re).ListScoped(cur, TierLong)
	if len(got) != 1 {
		t.Fatalf("after reopen: %v", got)
	}
}

// W1: the reparse helper recognises symlinks, junctions (irregular mode or the
// reparse attribute) and refuses nothing else.
func TestIsLinkLike(t *testing.T) {
	cases := []struct {
		name  string
		mode  os.FileMode
		attrs uint32
		want  bool
	}{
		{"regular", 0o644, 0, false},
		{"dir", os.ModeDir | 0o755, 0, false},
		{"symlink", os.ModeSymlink, 0, true},
		{"irregular junction", os.ModeIrregular, 0, true},
		{"reparse attribute on a dir", os.ModeDir, fileAttributeReparsePoint, true},
		{"other attributes", os.ModeDir, 0x10 | 0x20, false},
	}
	for _, c := range cases {
		if got := isLinkLike(c.mode, c.attrs); got != c.want {
			t.Errorf("%s: isLinkLike = %v, want %v", c.name, got, c.want)
		}
	}
}

// W1: a "summaries" directory junction must not let a prune reach outside the
// export directory (Windows only: mklink /J needs no privileges).
func TestExportPruneRefusesJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("directory junctions exist only on Windows")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim-deadbeef.md")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "summaries"), outside).CombinedOutput()
	if err != nil {
		t.Skipf("mklink /J unavailable: %v: %s", err, out)
	}
	removed, err := removeExportedFile(dir, "summaries/victim-deadbeef.md")
	if err != nil || removed {
		t.Fatalf("removeExportedFile through a junction = %v, %v; want false, nil", removed, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file behind the junction was deleted: %v", err)
	}
}

// W2: Windows folds case when matching a legacy alias; other systems do not.
func TestMatchLegacyName(t *testing.T) {
	set := map[string]bool{"MyApp": true}
	if got, ok := matchLegacyName("windows", set, "myapp"); !ok || got != "MyApp" {
		t.Errorf("windows: got %q, %v; want MyApp, true", got, ok)
	}
	if _, ok := matchLegacyName("linux", set, "myapp"); ok {
		t.Error("linux must stay case-sensitive")
	}
	if _, ok := matchLegacyName("darwin", set, "myapp"); ok {
		t.Error("darwin must stay case-sensitive")
	}
	if got, ok := matchLegacyName("linux", set, "MyApp"); !ok || got != "MyApp" {
		t.Errorf("exact match failed: %q %v", got, ok)
	}
}

// W3: generated names use only [A-Za-z0-9._-]; the prune matcher agrees.
func TestGeneratedNamesSafeCharset(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9._-]+-[0-9a-f]{8}\.md$`)
	for _, id := range []string{"a:b", "foo:bar", "ünï code", "a b", "x$y#z", "../../etc"} {
		n := safeBlockFilename(id)
		if !re.MatchString(n) || !isExportedFileName(n) {
			t.Errorf("safeBlockFilename(%q) = %q outside the safe set", id, n)
		}
	}
	n := safeSummaryFilename(time.Unix(1700000000, 0), "conv:1 é")
	if !isExportedFileName("summaries/" + n) {
		t.Errorf("summary name %q not accepted", n)
	}
	for _, bad := range []string{"foo:bar-0123abcd.md", "summaries/a:b-0123abcd.md", "a b-0123abcd.md", "é-0123abcd.md", "a\\b-0123abcd.md"} {
		if isExportedFileName(bad) {
			t.Errorf("isExportedFileName(%q) = true", bad)
		}
	}
}

// N2: a prune that is refused because of a symlink or reparse point warns once
// per run, naming the path and the reason, however many files were skipped.
func TestExportPruneWarnsOncePerRunOnLinkRefusal(t *testing.T) {
	s, _ := seedExportBlocks(t)
	dir := t.TempDir()
	outside := t.TempDir()
	for _, n := range []string{"a-deadbeef.md", "b-deadbeef.md"} {
		if err := os.WriteFile(filepath.Join(outside, n), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "summaries")); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName),
		[]byte(`{"version":1,"files":{"all":["summaries/a-deadbeef.md","summaries/b-deadbeef.md"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged []string
	old := warnf
	warnf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	defer func() { warnf = old }()

	_, _ = s.ExportWithOptions(dir, ExportOptions{})
	if len(logged) != 1 {
		t.Fatalf("%d warnings, want 1: %v", len(logged), logged)
	}
	if !strings.Contains(logged[0], "2 stale file(s)") || !strings.Contains(logged[0], "summaries") ||
		!strings.Contains(logged[0], "reparse point") {
		t.Errorf("warning lacks the count, path or reason: %s", logged[0])
	}
}
