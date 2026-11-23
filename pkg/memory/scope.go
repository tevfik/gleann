package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

// DetectRepoScope returns the memory scope of the current working directory:
// the normalized git remote origin, else the repository root name, else the
// working directory basename, else "global".
//
// It is the default scope of the MCP memory tools and of the memory that
// gleann ask, chat and the TUI inject. The working directory is read on every
// call (about 3 ms), so changing directory takes effect immediately and no
// stale scope is cached.
func DetectRepoScope() string {
	return DetectRepoScopeAt("")
}

// DetectRepoScopeAt is DetectRepoScope for an explicit directory. An empty dir
// means the current working directory. Tests use it to check repository
// detection without changing the process working directory.
func DetectRepoScopeAt(dir string) string {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "global"
		}
		dir = wd
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	// 1. The git remote origin is the most stable, portable scope.
	if out, err := gitOutput(dir, "config", "--get", "remote.origin.url"); err == nil {
		if raw := strings.TrimSpace(string(out)); raw != "" {
			return NormalizeGitRemote(raw)
		}
	}
	// 2. A repository without a remote is identified by its root name.
	if out, err := gitOutput(dir, "rev-parse", "--show-toplevel"); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return filepath.Base(top)
		}
	}
	// 3. A plain directory falls back to its own name.
	if base := filepath.Base(dir); base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return "global"
}

// gitOutput runs git in dir so detection does not depend on the process
// working directory.
func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// NormalizeGitRemote turns a git remote URL into a memory scope such as
// "github.com/org/repo". It accepts SCP (git@host:org/repo.git), https, http
// and ssh URLs, and keeps the normalization used by the MCP memory tools so
// stored scopes stay valid.
func NormalizeGitRemote(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "ssh://")
	s = strings.Replace(s, ":", "/", 1)
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	return s
}

// MemoryContextSetter is implemented by chat sessions that accept injected
// memory context (pkg/gleann.LeannChat satisfies it).
type MemoryContextSetter interface {
	SetMemoryContext(string)
}

// InjectRepoContext replaces target's injected memory context with the blocks
// visible to the current repository: global blocks, the repository scope and
// its path ancestors. Nothing changes when target or mgr is nil, or when the
// context cannot be built. A successful build with no blocks clears the
// previously injected context, so forgotten memories stop being injected.
func InjectRepoContext(target MemoryContextSetter, mgr *Manager) {
	if isNilSetter(target) || mgr == nil {
		return
	}
	cw, err := mgr.BuildRepoContext()
	if err != nil {
		return
	}
	target.SetMemoryContext(cw.Render())
}

func isNilSetter(target MemoryContextSetter) bool {
	if target == nil {
		return true
	}
	v := reflect.ValueOf(target)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}
