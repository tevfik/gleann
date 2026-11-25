package memory

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// Collision-safe scope prefixes introduced in v1.22 (finding H7).
//
// Before v1.22 a git repository without an origin remote scoped its memory to
// the bare toplevel basename and a plain directory to its own basename, so
// /tmp/app (not a repository) and /work/app (a remote-less clone) shared one
// scope named "app" and each saw the other's memory. The prefixes plus a hash
// of the canonical path make the two cases distinguishable:
//
//	"local:<base>-<8hex>"  git repository without a remote, hashed toplevel path
//	"dir:<base>-<8hex>"    plain directory, hashed working-directory path
//
// Remote-backed repositories keep the unchanged normalized remote
// ("github.com/org/repo").
const (
	localScopePrefix = "local:"
	dirScopePrefix   = "dir:"
)

// DetectRepoScope returns the memory scope of the current working directory:
// the normalized git remote origin, else a collision-safe scoped name for a
// remote-less repository ("local:<base>-<hash>"), else one for a plain
// directory ("dir:<base>-<hash>"), else "global".
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
	// Resolve symlinks (and Windows 8.3 short names) so every spelling of one
	// directory hashes to one scope.
	dir = canonicalDir(dir)

	// 1. The git remote origin is the most stable, portable scope.
	if out, err := gitOutput(dir, "config", "--get", "remote.origin.url"); err == nil {
		if raw := strings.TrimSpace(string(out)); raw != "" {
			return NormalizeGitRemote(raw)
		}
	}
	// 2. A repository without a remote is identified by its root path: the
	// "local:" prefix keeps it apart from plain directories and the path hash
	// apart from other repositories that share its folder name (finding H7).
	if out, err := gitOutput(dir, "rev-parse", "--show-toplevel"); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return prefixedScope(localScopePrefix, top)
		}
	}
	// 3. A plain directory is identified by its own path ("dir:" prefix).
	return prefixedScope(dirScopePrefix, dir)
}

// prefixedScope builds "<prefix><basename>-<hash>" for path, where hash is
// eight hex digits of the canonical (cleaned, absolute) path. Two directories
// with the same folder name therefore always get different scopes, and the
// prefix separates remote-less repositories from plain directories. A path with
// no usable basename (the filesystem root) has no scope: "global".
func prefixedScope(prefix, path string) string {
	path = canonicalDir(path)
	base := filepath.Base(path)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "global"
	}
	return prefix + base + "-" + keyHash(pathHashKey(runtime.GOOS, path))
}

// canonicalDir returns the absolute, symlink-resolved, cleaned form of p. On
// Windows EvalSymlinks also expands 8.3 short names (RUNNER~1) and, as does
// macOS, resolves links such as /var -> /private/var, so two spellings of one
// directory canonicalise to the same string. A path that cannot be resolved
// (it does not exist) keeps its absolute, cleaned form.
func canonicalDir(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p)
}

// pathHashKey turns a canonical directory path into the string that is hashed
// for a scope, so the hash depends neither on the separator style nor, where
// the filesystem ignores case, on letter case.
//
//   - windows: separators become "/" and the whole path is lower-cased. NTFS is
//     case-insensitive, so C:\Users\X and c:\users\x are one directory.
//   - darwin and others: no case folding. The default APFS is case-insensitive
//     but case-sensitive volumes exist, and EvalSymlinks does not canonicalise
//     case there, so folding could merge two distinct directories; a rare
//     case-only respelling on darwin gets a separate scope instead (safe).
//   - separators are only rewritten on Windows: a backslash is a legal file
//     name character elsewhere.
//
// goos is a parameter so tests can exercise every rule on any host.
func pathHashKey(goos, p string) string {
	if goos == "windows" {
		p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	}
	return path.Clean(p)
}

// matchLegacyName looks base up in the legacy-alias set. On Windows the match
// folds case with the same rule as pathHashKey, because the on-disk basename
// a scope is derived from can differ in case from the name typed when the old
// block was written. It returns the spelling stored in the set, which is the
// one the blocks carry. goos is a parameter so tests run on any host.
func matchLegacyName(goos string, set map[string]bool, base string) (string, bool) {
	if set[base] {
		return base, true
	}
	if goos != "windows" {
		return "", false
	}
	fold := strings.ToLower(base)
	var found string
	for name := range set {
		if strings.ToLower(name) == fold && (found == "" || name < found) {
			found = name
		}
	}
	return found, found != ""
}

// legacyAliasScope is the pre-v1.22 bare-basename scope a collision-safe scope
// was derived from: "local:app-1a2b3c4d" and "dir:app-1a2b3c4d" both come from
// a directory called "app". It is only used to keep blocks stored under that
// old name readable (see migrateLegacyScopeNames); remote-backed and explicitly
// scoped names have no alias.
func legacyAliasScope(scope string) (string, bool) {
	var rest string
	switch {
	case strings.HasPrefix(scope, localScopePrefix):
		rest = strings.TrimPrefix(scope, localScopePrefix)
	case strings.HasPrefix(scope, dirScopePrefix):
		rest = strings.TrimPrefix(scope, dirScopePrefix)
	default:
		return "", false
	}
	i := strings.LastIndex(rest, "-")
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
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

// NormalizeScope canonicalizes a memory scope identifier: it trims surrounding
// whitespace and slashes so "/github.com/org/repo/" and "github.com/org/repo"
// name the same scope. Case is preserved (scopes are matched exactly by
// filterScope). An empty scope stays empty and means "global".
func NormalizeScope(scope string) string {
	return strings.Trim(strings.TrimSpace(scope), "/")
}

// IsAllScopesSentinel reports whether scope is the cross-scope sentinel "all"
// or "*" (case-insensitive, surrounding whitespace trimmed). Read paths map the
// sentinel to the empty (unrestricted) scope; failure add paths map it to a
// global block. The sentinel is never a recordable scope name, and it must
// never bypass a restricted target (callers enforce that before mapping).
func IsAllScopesSentinel(scope string) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "all", "*":
		return true
	default:
		return false
	}
}

// normalizeBlockScopes canonicalizes a block's scope/repo identifiers before
// persistence: slash/whitespace padding is stripped and the all-scopes
// sentinel is never recordable — it becomes the global scope "". Every write
// path applies it so normalized reads always match stored rows (v3 repair,
// finding 1).
func normalizeBlockScopes(b *Block) {
	b.Scope = NormalizeScope(b.Scope)
	if IsAllScopesSentinel(b.Scope) {
		b.Scope = ""
	}
	b.Repo = NormalizeScope(b.Repo)
	if IsAllScopesSentinel(b.Repo) {
		b.Repo = ""
	}
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
	_ = InjectRepoContextErr(target, mgr)
}

// InjectRepoContextErr is InjectRepoContext that also reports why the context
// could not be built, so an interactive caller can tell the user. On error the
// target keeps its previous memory context.
func InjectRepoContextErr(target MemoryContextSetter, mgr *Manager) error {
	if isNilSetter(target) || mgr == nil {
		return nil
	}
	cw, err := mgr.BuildRepoContext()
	if err != nil {
		return fmt.Errorf("building repository memory context: %w", err)
	}
	target.SetMemoryContext(cw.Render())
	return nil
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
