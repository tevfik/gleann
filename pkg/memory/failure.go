package memory

import (
	"crypto/sha256"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Failure memory records a command that failed together with the fix the
// agent found, so the next session does not repeat the same mistake.
//
// A failure is an ordinary memory block with label "failure" (there is no
// tier-independent "kind" in block.go). The structured fields live in the
// block's Metadata under the documented keys below; the human summary is the
// block Content (the raw error text), and Paths/Symbols use the existing
// provenance fields. The block is stored in the long tier so it survives
// across sessions and is never auto-archived away.

const (
	// FailureLabel marks a block as a failure memory.
	FailureLabel = "failure"

	// Metadata keys of a failure block. The normalized signature
	// (FailureMetaSig) is the deduplication key together with the normalized
	// command; the raw command and error are kept for display.
	FailureMetaCommand = "failure.command"
	FailureMetaError   = "failure.error"
	FailureMetaFix     = "failure.fix"
	FailureMetaSig     = "failure.signature"
)

// Failure is the structured form of a failure memory. The recorded block
// carries Command, Error and Fix in its Metadata and Paths/Symbols in the
// existing provenance fields. Scope isolates the failure to a repository or
// conversation; Repo is the provenance repository identifier. When Scope is
// empty but Repo is set, Scope defaults to Repo (see resolveFailureScopeRepo).
type Failure struct {
	Scope   string
	Repo    string
	Command string
	Error   string
	Fix     string
	Paths   []string
	Symbols []string
}

// RecordFailure stores a failure memory, deduplicating by normalized error
// signature plus command (and scope): recording the same failure again
// reinforces the existing block (Confirms++, the observation count) and unions
// its paths and symbols instead of adding a second block. The first recording
// sets Confirms to 1, so two recordings of the same failure leave one block
// with Confirms 2. A Failure carrying Scope/Repo records a scoped block; the
// empty-scope case is byte-for-byte compatible with earlier releases.
func (m *Manager) RecordFailure(f Failure) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordFailureLocked(f)
}

// RecordFailureScoped stores a failure memory bound to an explicit scope/repo.
// Non-empty scope/repo arguments override f.Scope/f.Repo; empty arguments fall
// back to the fields already set on f. The scope is normalized and folded into
// the block ID so the same command+error recorded in two scopes stays two
// blocks, while repeats within one scope deduplicate. An empty scope records a
// global block. Callers that want the current repository as the default scope
// (MCP tools, the CLI) pass DetectRepoScope() themselves.
func (m *Manager) RecordFailureScoped(scope, repo string, f Failure) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope != "" {
		f.Scope = scope
	}
	if repo != "" {
		f.Repo = repo
	}
	return m.recordFailureLocked(f)
}

// recordFailureLocked is the shared write path of RecordFailure and
// RecordFailureScoped. It must be called with m.mu held for writing.
func (m *Manager) recordFailureLocked(f Failure) (*Block, error) {
	f.Scope, f.Repo = resolveFailureScopeRepo(f.Scope, f.Repo)
	block := failureBlock(f)

	// The block ID is a hash of the normalized command, error signature and
	// scope, so a repeat of the same failure is a single lookup, not a scan.
	if existing, err := m.store.Get(block.ID); err == nil && existing.Label == FailureLabel {
		mergeDuplicate(existing, block)
		if err := m.store.Update(existing); err != nil {
			return nil, err
		}
		return existing, nil
	}

	// Read fallback for rows older releases wrote with the legacy unscoped ID
	// formula (v3 repair, finding 5). Only unscoped failures can carry legacy
	// IDs, and because the old formula was injectable the row must really be
	// the same failure (normalized command + signature, unscoped) before it
	// is reinforced; the legacy ID is kept so the row is updated, not
	// duplicated.
	if f.Scope == "" {
		sig := block.Metadata[FailureMetaSig]
		if legacyID := legacyFailureBlockID(f.Command, sig); legacyID != block.ID {
			if existing, err := m.store.Get(legacyID); err == nil &&
				existing.Label == FailureLabel && existing.Scope == "" &&
				normalizeCommand(existing.Metadata[FailureMetaCommand]) == normalizeCommand(f.Command) &&
				existing.Metadata[FailureMetaSig] == sig {
				mergeDuplicate(existing, block)
				if err := m.store.Update(existing); err != nil {
					return nil, err
				}
				return existing, nil
			}
		}
	}

	if err := m.store.Add(block); err != nil {
		return nil, err
	}
	return block, nil
}

// resolveFailureScopeRepo normalizes the scope/repo pair of a failure. An empty
// scope with a non-empty repo defaults the scope to the repo, so a repo-tagged
// failure is isolated to that repo even when the caller only set Repo. The
// all-scopes sentinel is a query concept, never a recordable scope: a sentinel
// repo must not create a literal "all" scope (v3 repair, finding 2).
func resolveFailureScopeRepo(scope, repo string) (string, string) {
	scope = NormalizeScope(scope)
	repo = NormalizeScope(repo)
	if IsAllScopesSentinel(scope) {
		scope = ""
	}
	if IsAllScopesSentinel(repo) {
		repo = ""
	}
	if scope == "" && repo != "" {
		scope = repo
	}
	return scope, repo
}

// ListFailures returns all failure blocks, newest first.
func (m *Manager) ListFailures() ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.listFailures()
}

// FailuresFor returns the failures relevant to a task that names a command
// and/or a set of files. A failure matches when its command matches the query
// command (normalized, prefix in either direction) or when one of its paths
// overlaps the query paths. An empty command and no paths matches nothing.
func (m *Manager) FailuresFor(command string, paths []string) ([]Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.failuresFor(command, paths)
}

// ListFailuresScoped returns the failure blocks visible to a scope (global
// blocks plus the scope and its path ancestors), newest first. An empty scope
// returns every failure. The scope is normalized, so "repo-a", "repo-a/" and
// "/repo-a" name the same scope.
func (m *Manager) ListFailuresScoped(scope string) ([]Block, error) {
	scope = NormalizeScope(scope)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.listFailuresScoped(scope)
}

// FailuresForScoped returns the failures visible to a scope that match a
// command, a set of file paths and/or a set of symbols. paths and symbols are
// distinct queries: paths match the block's provenance file paths, symbols
// match its provenance symbol names. An empty scope searches every failure.
// The scope is normalized like ListFailuresScoped.
func (m *Manager) FailuresForScoped(scope, command string, paths, symbols []string) ([]Block, error) {
	scope = NormalizeScope(scope)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.failuresForScoped(scope, command, paths, symbols)
}

// BuildTaskContext compiles the scope context and, when command or paths
// name what the task is about, attaches the matching failures in their own
// section. The result is capped at the injection budget like BuildContext.
func (m *Manager) BuildTaskContext(scope, command string, paths []string) (*ContextWindow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cw, err := m.buildContextLocked()
	if err != nil {
		return nil, err
	}
	if scope != "" {
		cw.ShortTerm = filterScopeAliases(cw.ShortTerm, scope, m.scopeAliases(scope))
		cw.MediumTerm = filterScopeAliases(cw.MediumTerm, scope, m.scopeAliases(scope))
		cw.LongTerm = filterScopeAliases(cw.LongTerm, scope, m.scopeAliases(scope))
	}
	if command != "" || len(paths) > 0 {
		cw.Failures, err = m.store.failuresFor(command, paths)
		if err != nil {
			return nil, err
		}
	}
	m.applyBudget(cw)
	return cw, nil
}

// BuildTaskContextFor is BuildTaskContext with a distinct symbols query and a
// scope-aware failure lookup: both the general context and the surfaced
// failures are restricted to the blocks visible to scope. paths and symbols are
// separate parameters and are never folded into one another. With no command,
// paths or symbols it returns the plain scoped context (no failure section).
// The scope is normalized like ListFailuresScoped.
func (m *Manager) BuildTaskContextFor(scope, command string, paths, symbols []string) (*ContextWindow, error) {
	scope = NormalizeScope(scope)
	m.mu.RLock()
	defer m.mu.RUnlock()

	cw, err := m.buildContextLocked()
	if err != nil {
		return nil, err
	}
	if scope != "" {
		cw.ShortTerm = filterScopeAliases(cw.ShortTerm, scope, m.scopeAliases(scope))
		cw.MediumTerm = filterScopeAliases(cw.MediumTerm, scope, m.scopeAliases(scope))
		cw.LongTerm = filterScopeAliases(cw.LongTerm, scope, m.scopeAliases(scope))
	}
	if command != "" || len(paths) > 0 || len(symbols) > 0 {
		cw.Failures, err = m.store.failuresForScoped(scope, command, paths, symbols)
		if err != nil {
			return nil, err
		}
	}
	m.applyBudget(cw)
	return cw, nil
}

// failureBlock builds the failure block for a Failure, computing the
// normalized signature and command that drive deduplication. f.Scope/f.Repo
// must already be normalized (see resolveFailureScopeRepo).
func failureBlock(f Failure) *Block {
	sig := NormalizeErrorSignature(f.Error)
	content := strings.TrimSpace(f.Error)
	if content == "" {
		content = strings.TrimSpace(f.Command) + " failed"
	}
	cmd := strings.TrimSpace(f.Command)
	return &Block{
		ID:       failureBlockID(cmd, sig, f.Scope),
		Tier:     TierLong,
		Label:    FailureLabel,
		Content:  content,
		Source:   "failure",
		Scope:    f.Scope,
		Repo:     f.Repo,
		Paths:    f.Paths,
		Symbols:  f.Symbols,
		Confirms: 1, // the first observation counts as one confirmation
		Metadata: map[string]string{
			FailureMetaCommand: cmd,
			FailureMetaError:   f.Error,
			FailureMetaFix:     f.Fix,
			FailureMetaSig:     sig,
		},
	}
}

// failureBlockID derives the block ID from the normalized command, error
// signature and scope, so two recordings of the same failure in the same scope
// share an ID while failures with the same raw error text but different
// commands (or different scopes) stay distinct.
//
// Every tuple — including the empty scope — uses the domain-separated,
// length-prefixed encoding, so no two (command, signature, scope) tuples can
// share an ID even when the fields contain '|', ':' or NUL bytes (v3 repair,
// finding 5: the old "failure|"+command+"|"+signature formula for unscoped
// failures was injectable). Rows written by older releases keep their legacy
// IDs and are still found via legacyFailureBlockID on the write path.
func failureBlockID(command, sig, scope string) string {
	h := sha256.New()
	normCmd := normalizeCommand(command)
	fmt.Fprintf(h, "failure-scoped\x00%d:%s\x00%d:%s\x00%d:%s",
		len(normCmd), normCmd, len(sig), sig, len(scope), scope)
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// legacyFailureBlockID reproduces the pre-v3 unscoped ID formula. It is used
// ONLY as a read fallback to find rows written by older releases — never for
// new writes.
func legacyFailureBlockID(command, sig string) string {
	sum := sha256.Sum256([]byte("failure|" + normalizeCommand(command) + "|" + sig))
	return fmt.Sprintf("%x", sum)[:16]
}

// listFailures returns all failure blocks (label "failure"), newest first.
func (s *Store) listFailures() ([]Block, error) {
	all, err := s.List(TierLong)
	if err != nil {
		return nil, err
	}
	out := make([]Block, 0, len(all))
	for _, b := range all {
		if b.Label == FailureLabel {
			out = append(out, b)
		}
	}
	return out, nil
}

// listFailuresScoped returns failure blocks visible to a scope (global + scope
// chain), or all failures when scope is empty.
func (s *Store) listFailuresScoped(scope string) ([]Block, error) {
	failures, err := s.listFailures()
	if err != nil {
		return nil, err
	}
	if scope == "" {
		return failures, nil
	}
	return filterScopeAliases(failures, scope, s.scopeAliases(scope)), nil
}

// failuresFor returns the failures matching command and/or paths. See
// Manager.FailuresFor.
func (s *Store) failuresFor(command string, paths []string) ([]Block, error) {
	failures, err := s.listFailures()
	if err != nil {
		return nil, err
	}
	return matchFailures(failures, command, paths, nil, ""), nil
}

// failuresForScoped returns the failures visible to a scope matching command,
// paths and/or symbols. See Manager.FailuresForScoped.
func (s *Store) failuresForScoped(scope, command string, paths, symbols []string) ([]Block, error) {
	scope = NormalizeScope(scope)
	failures, err := s.listFailures()
	if err != nil {
		return nil, err
	}
	if scope != "" {
		failures = filterScopeAliases(failures, scope, s.scopeAliases(scope))
	}
	return matchFailures(failures, command, paths, symbols, scope), nil
}

// matchFailures selects the failures relevant to a task. A failure matches when
// its command matches the query command (normalized, prefix in either
// direction), OR one of its paths overlaps the query paths, OR one of its
// symbols matches a query symbol. Command, path and symbol are independent
// signals; any one of them surfacing a failure is enough. scope drives the
// base-name path fallback guard (see basenameAllowed).
func matchFailures(failures []Block, command string, paths, symbols []string, scope string) []Block {
	cmd := normalizeCommand(command)
	var out []Block
	for _, b := range failures {
		if cmd != "" && commandMatches(b.Metadata[FailureMetaCommand], cmd) {
			out = append(out, b)
			continue
		}
		if pathOverlaps(b.Paths, paths, basenameAllowed(b, scope)) {
			out = append(out, b)
			continue
		}
		if symbolOverlaps(b.Symbols, symbols) {
			out = append(out, b)
		}
	}
	return out
}

// commandMatches reports whether the stored command and the query command
// match under normalization, as a prefix in either direction.
func commandMatches(stored, query string) bool {
	s, q := normalizeCommand(stored), query
	if s == "" || q == "" {
		return false
	}
	return strings.HasPrefix(s, q) || strings.HasPrefix(q, s)
}

// basenameAllowed reports whether base-name-only path fallback may match block
// b for a query in the given scope. Unscoped queries keep the legacy basename
// behavior; scoped queries only allow it when the block belongs to the queried
// repo/scope, so a basename like main.go never leaks from repo A into repo B.
func basenameAllowed(b Block, scope string) bool {
	scope = NormalizeScope(scope)
	return scope == "" || b.Scope == scope || b.Repo == scope
}

// pathOverlaps reports whether any stored path matches any query path by
// cleaned, slash-normalized path, and (when allowBasename) by base name — the
// same shape matchesProvenance uses.
func pathOverlaps(storedPaths, queryPaths []string, allowBasename bool) bool {
	if len(queryPaths) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(queryPaths)*2)
	for _, p := range queryPaths {
		clean := filepath.ToSlash(filepath.Clean(p))
		set[clean] = struct{}{}
		if allowBasename {
			set[path.Base(clean)] = struct{}{}
		}
	}
	for _, p := range storedPaths {
		clean := filepath.ToSlash(filepath.Clean(p))
		if _, ok := set[clean]; ok {
			return true
		}
		if allowBasename {
			if _, ok := set[path.Base(clean)]; ok {
				return true
			}
		}
	}
	return false
}

// symbolOverlaps reports whether any stored symbol matches any query symbol:
// exact normalized equality, or a whole-segment FQN suffix in either direction
// (pkg.Foo matches Foo, and github.com/org/repo/pkg.Foo). This mirrors
// matchesProvenance's exact-or-last-segment symbol match, generalized to full
// trailing segments. Single-character suffixes and raw substrings never match,
// guarding against false positives.
func symbolOverlaps(storedSymbols, querySymbols []string) bool {
	if len(querySymbols) == 0 {
		return false
	}
	for _, q := range querySymbols {
		q = normalizeSymbol(q)
		if q == "" {
			continue
		}
		for _, s := range storedSymbols {
			s = normalizeSymbol(s)
			if s == "" {
				continue
			}
			if s == q || fqnSuffix(s, q) || fqnSuffix(q, s) {
				return true
			}
		}
	}
	return false
}

// fqnSuffix reports whether long ends with short at a segment boundary
// (immediately preceded by "." or "/"), with short at least two characters. It
// matches whole trailing segments of a fully-qualified name, never raw
// substrings or single characters.
func fqnSuffix(long, short string) bool {
	if len(short) < 2 || len(long) <= len(short) {
		return false
	}
	if !strings.HasSuffix(long, short) {
		return false
	}
	sep := long[len(long)-len(short)-1]
	return sep == '.' || sep == '/'
}

// normalizeSymbol trims a symbol and unifies FQN separators (Rust "::", TS/JS
// "#") to "." so suffix matching works across languages.
func normalizeSymbol(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "::", ".")
	s = strings.ReplaceAll(s, "#", ".")
	return s
}

// normalizeCommand trims, collapses whitespace and lowercases a command for
// stable comparison.
func normalizeCommand(s string) string {
	return strings.ToLower(collapseSpace(s))
}

var (
	isoTimeRe  = regexp.MustCompile(`\b\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?\b`)
	hexRe      = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	pathToken  = regexp.MustCompile(`\S*[/\\]\S*`)
	lineColRe  = regexp.MustCompile(`:\d+(:\d+)?\b`)
	spaceRunRe = regexp.MustCompile(`\s+`)
)

// NormalizeErrorSignature strips the incidental parts of an error text — file
// paths, line/column numbers, hex addresses and timestamps — so two
// recordings of the same failure that differ only in those details produce
// the same signature and deduplicate. The result is lowercased and
// whitespace-collapsed.
func NormalizeErrorSignature(s string) string {
	s = collapseSpace(s)
	s = isoTimeRe.ReplaceAllString(s, "TIME")
	s = hexRe.ReplaceAllString(s, "HEX")
	s = pathToken.ReplaceAllString(s, "PATH")
	s = lineColRe.ReplaceAllString(s, "")
	return strings.ToLower(collapseSpace(s))
}

// collapseSpace trims s and collapses runs of whitespace to single spaces.
func collapseSpace(s string) string {
	return strings.TrimSpace(spaceRunRe.ReplaceAllString(s, " "))
}
