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
		want   string
	}{
		{"scp remote", "git@github.com:org/repo-a.git", true, "github.com/org/repo-a"},
		{"https remote", "https://github.com/org/repo-a.git", true, "github.com/org/repo-a"},
		{"ssh remote", "ssh://github.com/org/repo-a.git", true, "github.com/org/repo-a"},
		// A repository without a remote falls back to the toplevel basename.
		{"repo without remote", "", true, ""},
		// A plain directory falls back to its own basename.
		{"non-git directory", "", false, ""},
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
			want := tc.want
			if want == "" {
				want = filepath.Base(dir)
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
