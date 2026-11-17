package memory

import (
	"crypto/sha256"
	"fmt"
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
// existing provenance fields.
type Failure struct {
	Command string
	Error   string
	Fix     string
	Paths   []string
	Symbols []string
}

// RecordFailure stores a failure memory, deduplicating by normalized error
// signature plus command: recording the same failure again reinforces the
// existing block (Confirms++, the observation count) and unions its paths and
// symbols instead of adding a second block. The first recording sets Confirms
// to 1, so two recordings of the same failure leave one block with Confirms 2.
func (m *Manager) RecordFailure(f Failure) (*Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	block := failureBlock(f)

	// The block ID is a hash of the normalized command and error signature,
	// so a repeat of the same failure is a single lookup, not a scan.
	if existing, err := m.store.Get(block.ID); err == nil && existing.Label == FailureLabel {
		mergeDuplicate(existing, block)
		if err := m.store.Update(existing); err != nil {
			return nil, err
		}
		return existing, nil
	}

	if err := m.store.Add(block); err != nil {
		return nil, err
	}
	return block, nil
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
		cw.ShortTerm = filterScope(cw.ShortTerm, scope)
		cw.MediumTerm = filterScope(cw.MediumTerm, scope)
		cw.LongTerm = filterScope(cw.LongTerm, scope)
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

// failureBlock builds the failure block for a Failure, computing the
// normalized signature and command that drive deduplication.
func failureBlock(f Failure) *Block {
	sig := NormalizeErrorSignature(f.Error)
	content := strings.TrimSpace(f.Error)
	if content == "" {
		content = strings.TrimSpace(f.Command) + " failed"
	}
	cmd := strings.TrimSpace(f.Command)
	return &Block{
		ID:       failureBlockID(cmd, sig),
		Tier:     TierLong,
		Label:    FailureLabel,
		Content:  content,
		Source:   "failure",
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

// failureBlockID derives the block ID from the normalized command and error
// signature, so two recordings of the same failure share an ID while failures
// with the same raw error text but different commands stay distinct (the
// default content hash would collapse the latter).
func failureBlockID(command, sig string) string {
	h := sha256.New()
	h.Write([]byte("failure|"))
	h.Write([]byte(normalizeCommand(command)))
	h.Write([]byte("|"))
	h.Write([]byte(sig))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
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

// failuresFor returns the failures matching command and/or paths. See
// Manager.FailuresFor.
func (s *Store) failuresFor(command string, paths []string) ([]Block, error) {
	failures, err := s.listFailures()
	if err != nil {
		return nil, err
	}
	cmd := normalizeCommand(command)
	var out []Block
	for _, b := range failures {
		if cmd != "" && commandMatches(b.Metadata[FailureMetaCommand], cmd) {
			out = append(out, b)
			continue
		}
		if pathOverlaps(b.Paths, paths) {
			out = append(out, b)
		}
	}
	return out, nil
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

// pathOverlaps reports whether any stored path matches any query path, by
// cleaned path or by base name (the same shape matchesProvenance uses).
func pathOverlaps(storedPaths, queryPaths []string) bool {
	if len(queryPaths) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(queryPaths)*2)
	for _, p := range queryPaths {
		clean := filepath.Clean(p)
		set[clean] = struct{}{}
		set[filepath.Base(clean)] = struct{}{}
	}
	for _, p := range storedPaths {
		clean := filepath.Clean(p)
		if _, ok := set[clean]; ok {
			return true
		}
		if _, ok := set[filepath.Base(clean)]; ok {
			return true
		}
	}
	return false
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
