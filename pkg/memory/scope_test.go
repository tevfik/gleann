package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeGitRemote(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		want   string
	}{
		{"scp", "git@github.com:org/repo.git", "github.com/org/repo"},
		{"scp with whitespace", "  git@github.com:org/repo.git\n", "github.com/org/repo"},
		{"https", "https://github.com/org/repo.git", "github.com/org/repo"},
		{"http without suffix", "http://github.com/org/repo", "github.com/org/repo"},
		{"ssh", "ssh://github.com/org/repo.git", "github.com/org/repo"},
		{"already normalized", "github.com/org/repo", "github.com/org/repo"},
		{"nested group", "git@gitlab.com:group/sub/repo.git", "gitlab.com/group/sub/repo"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeGitRemote(tc.remote); got != tc.want {
				t.Errorf("NormalizeGitRemote(%q) = %q, want %q", tc.remote, got, tc.want)
			}
		})
	}
}

// initScopeRepo creates a git repository in a temp dir, optionally with an
// origin remote, and returns its path.
func initScopeRepo(t *testing.T, remote string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL="+os.DevNull,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if remote != "" {
		runGit("remote", "add", "origin", remote)
	}
	return dir
}

func TestDetectRepoScopeAt(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		init   bool
		prefix string // "" = expect the exact remote scope
	}{
		{"scp remote", "git@github.com:org/repo-a.git", true, ""},
		{"https remote", "https://github.com/org/repo-a.git", true, ""},
		{"ssh remote", "ssh://github.com/org/repo-a.git", true, ""},
		// A repository without a remote gets a collision-safe "local:" scope
		// built from the toplevel basename and a hash of its path (H7).
		{"repo without remote", "", true, "local:"},
		// A plain directory gets the same treatment with a "dir:" prefix.
		{"non-git directory", "", false, "dir:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("git not installed")
			}
			dir := t.TempDir()
			if tc.init {
				dir = initScopeRepo(t, tc.remote)
			}
			want := tc.remote
			if tc.prefix != "" {
				// git reports the symlink-resolved toplevel; a plain
				// directory is hashed as given.
				hashPath := dir
				if tc.init {
					if resolved, err := filepath.EvalSymlinks(dir); err == nil {
						hashPath = resolved
					}
				}
				want = tc.prefix + filepath.Base(dir) + "-" + keyHash(hashPath)
			} else if want != "" {
				want = NormalizeGitRemote(want)
			}

			if got := DetectRepoScopeAt(dir); got != want {
				t.Errorf("DetectRepoScopeAt(%q) = %q, want %q", dir, got, want)
			}

			// DetectRepoScope must use identical semantics for the process cwd.
			t.Chdir(dir)
			if got := DetectRepoScope(); got != want {
				t.Errorf("DetectRepoScope() in %q = %q, want %q", dir, got, want)
			}
		})
	}
}

// Two directories that share a folder name must not share a memory scope, and
// a remote-less repository must not collide with a plain directory of the same
// name (finding H7). A subdirectory of a repository resolves to the toplevel
// scope, and a remote-backed repository keeps the normalized remote.
func TestDetectRepoScopeCollisionSafe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	mk := func(name string) string {
		t.Helper()
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	repoA := initScopeRepo(t, "") // remote-less repository
	repoB := initScopeRepo(t, "")
	plainA := mk(filepath.Join("left", "app"))
	plainB := mk(filepath.Join("right", "app")) // same basename, different path
	// Name a plain dir like the repos' toplevels to force the old collision.
	repoBase := filepath.Base(repoA)
	plainSame := mk(repoBase)

	scopes := map[string]string{
		"repoA":     DetectRepoScopeAt(repoA),
		"repoB":     DetectRepoScopeAt(repoB),
		"plainA":    DetectRepoScopeAt(plainA),
		"plainB":    DetectRepoScopeAt(plainB),
		"plainSame": DetectRepoScopeAt(plainSame),
	}
	seen := map[string]string{}
	for name, s := range scopes {
		if other, dup := seen[s]; dup {
			t.Errorf("scopes collide: %s and %s both = %q", other, name, s)
		}
		seen[s] = name
	}
	if !strings.HasPrefix(scopes["repoA"], "local:"+repoBase+"-") {
		t.Errorf("remote-less repo scope = %q, want local:%s-<hash>", scopes["repoA"], repoBase)
	}
	if !strings.HasPrefix(scopes["plainSame"], "dir:"+repoBase+"-") {
		t.Errorf("plain dir scope = %q, want dir:%s-<hash>", scopes["plainSame"], repoBase)
	}

	// A subdirectory of the repository resolves to the toplevel scope.
	sub := filepath.Join(repoA, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectRepoScopeAt(sub); got != scopes["repoA"] {
		t.Errorf("subdirectory scope = %q, want toplevel %q", got, scopes["repoA"])
	}

	// A remote wins over the path-based fallback, unchanged.
	remoteRepo := initScopeRepo(t, "git@github.com:org/repo-a.git")
	if got := DetectRepoScopeAt(remoteRepo); got != "github.com/org/repo-a" {
		t.Errorf("remote-backed scope = %q, want github.com/org/repo-a", got)
	}

	// The filesystem root has no usable basename: global.
	if got := prefixedScope(dirScopePrefix, string(filepath.Separator)); got != "global" {
		t.Errorf("prefixedScope(root) = %q, want global", got)
	}
}

// legacyAliasScope recovers the pre-v1.22 bare basename from a collision-safe
// scope, including basenames that themselves contain dashes.
func TestLegacyAliasScope(t *testing.T) {
	cases := []struct {
		scope string
		want  string
		ok    bool
	}{
		{"local:app-1a2b3c4d", "app", true},
		{"dir:app-1a2b3c4d", "app", true},
		{"local:my-app-1a2b3c4d", "my-app", true},
		{"dir:my-app-1a2b3c4d", "my-app", true},
		{"github.com/org/repo", "", false},
		{"app", "", false},
		{"local:-1a2b3c4d", "", false},
		{"local:app", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := legacyAliasScope(tc.scope)
		if got != tc.want || ok != tc.ok {
			t.Errorf("legacyAliasScope(%q) = %q,%v want %q,%v", tc.scope, got, ok, tc.want, tc.ok)
		}
	}
}

func TestInjectRepoContext_FiltersSiblingScopes(t *testing.T) {
	t.Chdir(initScopeRepo(t, "git@github.com:org/repo-a.git"))

	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := NewManager(store)

	for _, tc := range []struct {
		scope   string
		content string
	}{
		{"github.com/org/repo-a", "repo-a secret rule"},
		{"github.com/org/repo-b", "repo-b secret rule"},
		{"github.com/org", "org ancestor rule"},
		{"", "global user rule"},
	} {
		if _, err := mgr.AddScopedNote(tc.scope, TierLong, "rule", tc.content); err != nil {
			t.Fatal(err)
		}
	}

	rec := &recordingSetter{}
	InjectRepoContext(rec, mgr)

	if rec.calls != 1 {
		t.Fatalf("SetMemoryContext called %d times, want 1", rec.calls)
	}
	for _, want := range []string{"repo-a secret rule", "org ancestor rule", "global user rule"} {
		if !strings.Contains(rec.value, want) {
			t.Errorf("injected memory is missing %q:\n%s", want, rec.value)
		}
	}
	if strings.Contains(rec.value, "repo-b secret rule") {
		t.Errorf("injected memory leaked a sibling repository's block:\n%s", rec.value)
	}
}

func TestInjectRepoContext_ClearsWhenContextIsEmpty(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := NewManager(store)

	rec := &recordingSetter{value: "stale memory"}
	InjectRepoContext(rec, mgr)

	if rec.calls != 1 || rec.value != "" {
		t.Errorf("empty context should clear stale memory, calls=%d value=%q", rec.calls, rec.value)
	}
}

func TestInjectRepoContext_NilInputsAreNoOps(t *testing.T) {
	InjectRepoContext(nil, nil)
	InjectRepoContext(&recordingSetter{}, nil)
	InjectRepoContext(nil, NewManager(nil))

	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var typedNil *recordingSetter
	InjectRepoContext(typedNil, NewManager(store))
}

type recordingSetter struct {
	value string
	calls int
}

func (r *recordingSetter) SetMemoryContext(v string) {
	r.value = v
	r.calls++
}

func TestInjectRepoContextErr_ReportsBuildFailureAndKeepsContext(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	rec := &recordingSetter{value: "previous memory"}
	err = InjectRepoContextErr(rec, mgr)
	if err == nil || !strings.Contains(err.Error(), "building repository memory context") {
		t.Fatalf("err = %v, want the build failure", err)
	}
	if rec.calls != 0 || rec.value != "previous memory" {
		t.Errorf("a failed build must leave the context alone, calls=%d value=%q", rec.calls, rec.value)
	}
	if err := InjectRepoContextErr(nil, nil); err != nil {
		t.Errorf("nil inputs: %v", err)
	}
}
