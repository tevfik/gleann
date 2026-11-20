package memory

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
)

// nearDedupEnv disables near-duplicate merging on write: "off", "0", "false"
// or "no". Exact duplicates (same normalised content) are always merged.
const nearDedupEnv = "GLEANN_MEMORY_NEAR_DEDUP"

// dedupScanLimit bounds how many of the newest blocks of a scope and tier a
// write compares against. A bot that writes thousands of blocks per scope
// still pays a bounded cost per write.
const dedupScanLimit = 2000

// rowQuerier is the read side shared by *sql.DB and *sql.Tx, so the duplicate
// lookup runs inside Add's write transaction and, read-only, for dry runs.
type rowQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func nearDedupEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(nearDedupEnv))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}

// trimSet is the surrounding punctuation ignored when comparing contents.
const trimSet = ".,;:!?…\"'`"

// normalizeContent lowercases content, collapses whitespace runs to a single
// space and strips surrounding whitespace and sentence punctuation, so "Use
// Postgres.", "use  postgres" and " USE POSTGRES!" are the same fact.
func normalizeContent(content string) string {
	s := strings.Join(strings.Fields(strings.ToLower(content)), " ")
	return strings.Trim(s, trimSet+" ")
}

// blockIDFor hashes normalised content, scope and tier.
func blockIDFor(norm, scope string, tier Tier) string {
	h := sha256.New()
	h.Write([]byte(norm))
	h.Write([]byte("|"))
	h.Write([]byte(scope))
	h.Write([]byte("|"))
	h.Write([]byte(string(tier)))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// generateBlockID is the ID of a new block: a hash of the normalised content,
// scope and tier (see normalizeContent).
func generateBlockID(block *Block) string {
	return blockIDFor(normalizeContent(block.Content), block.Scope, block.Tier)
}

// legacyBlockID is the ID older versions assigned: only lowercase and trim, no
// whitespace or punctuation folding. Stored blocks keep these IDs, so Add
// looks them up as well instead of migrating the store.
func legacyBlockID(block *Block) string {
	return blockIDFor(strings.ToLower(strings.TrimSpace(block.Content)), block.Scope, block.Tier)
}

// isPolarityWord reports words that flip the meaning of a sentence. Two texts
// that differ in which of these they contain are a conflict (see
// Manager.checkContradictions), not a duplicate, whatever their token overlap.
func isPolarityWord(w string) bool {
	switch w {
	case "not", "no", "never", "cannot", "cant", "dont", "doesnt",
		"isnt", "arent", "wont", "shouldnt", "mustnt", "without",
		"disable", "disabled", "enable", "enabled", "always", "avoid",
		"true", "false", "deprecated", "removed", "added", "only":
		return true
	}
	return false
}

// dupProbe is the precomputed comparison form of a block being added.
type dupProbe struct {
	norm  string
	toks  map[string]struct{}
	flags map[string]struct{} // polarity words and tokens containing digits
	near  bool
}

func newDupProbe(content string, near bool) *dupProbe {
	p := &dupProbe{norm: normalizeContent(content), near: near}
	if near {
		p.toks = dedupTokens(content)
		p.flags = dupFlags(content)
	}
	return p
}

// dedupTokens is coverageTokens plus single-character words ("writer b",
// "plan 3"), which the search tokenizer drops but which can be the only
// difference between two facts. The articles "a" and "i" stay ignored.
func dedupTokens(content string) map[string]struct{} {
	set := coverageTokens(content, false)
	for _, w := range strings.FieldsFunc(strings.ToLower(content),
		func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) == 1 && w != "a" && w != "i" {
			set[w] = struct{}{}
		}
	}
	return set
}

// dupFlags returns the polarity words and digit-bearing tokens of content.
// Differing numbers ("port 8081" vs "port 9090") are different facts.
func dupFlags(content string) map[string]struct{} {
	words := strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(content, "'", "")),
		func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := map[string]struct{}{}
	for _, w := range words {
		if isPolarityWord(w) {
			out[w] = struct{}{}
			continue
		}
		for _, r := range w {
			if unicode.IsDigit(r) {
				out[w] = struct{}{}
				break
			}
		}
	}
	return out
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// jaccard returns |A and B| / |A or B| of two token sets, and |A or B|.
func jaccard(a, b map[string]struct{}) (float64, int) {
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0, 0
	}
	return float64(inter) / float64(union), union
}

// nearThreshold is the token Jaccard two texts must reach to be merged. It is
// stricter for short texts, where one differing word changes the meaning:
// a union of 3 tokens or fewer must match exactly, up to 6 needs 0.8, and
// longer texts need 0.7. On top of this, score requires the difference to be
// one-sided.
func nearThreshold(union int) float64 {
	switch {
	case union <= 3:
		return 1
	case union <= 6:
		return 0.8
	default:
		return 0.7
	}
}

// residual returns the tokens only in old and only in cur, after cancelling
// pairs where one token is a prefix (at least 4 letters) of the other, so
// "commit"/"committ" (a stemmer artefact) or "connect"/"connected" are not
// counted as different words.
func residual(old, cur map[string]struct{}) (onlyOld, onlyNew []string) {
	for t := range old {
		if _, ok := cur[t]; !ok {
			onlyOld = append(onlyOld, t)
		}
	}
	for t := range cur {
		if _, ok := old[t]; !ok {
			onlyNew = append(onlyNew, t)
		}
	}
	cancel := func(from, against []string) []string {
		var keep []string
		for _, t := range from {
			matched := false
			for _, u := range against {
				short, long := t, u
				if len(short) > len(long) {
					short, long = long, short
				}
				if len(short) >= 4 && strings.HasPrefix(long, short) {
					matched = true
					break
				}
			}
			if !matched {
				keep = append(keep, t)
			}
		}
		return keep
	}
	return cancel(onlyOld, onlyNew), cancel(onlyNew, onlyOld)
}

// score reports how well content (the stored text) duplicates the probe (the
// text being written): 2 for the same normalised content, 1+jaccard for a
// near duplicate, 0 for no match. supersedes is true when the probe is the
// stored text plus extra words, so the merge should keep the longer text.
//
// A near duplicate must differ one-sidedly: only extra words on one side. If
// each text has words the other lacks, it is a substitution ("billing
// service" vs "auth service") and may be a different fact, so it is never
// merged, whatever the Jaccard.
func (p *dupProbe) score(content string) (score float64, supersedes bool) {
	if normalizeContent(content) == p.norm {
		return 2, false
	}
	if !p.near || len(p.toks) == 0 {
		return 0, false
	}
	toks := dedupTokens(content)
	if len(toks) == 0 || !sameSet(p.flags, dupFlags(content)) {
		return 0, false
	}
	j, union := jaccard(p.toks, toks)
	if j < nearThreshold(union) {
		return 0, false
	}
	onlyStored, onlyNew := residual(toks, p.toks)
	if len(onlyStored) > 0 && len(onlyNew) > 0 {
		return 0, false
	}
	return 1 + j, len(onlyNew) > 0
}

// dedupEligible reports whether block takes part in content-based merging.
// Failure blocks keep their own signature dedup (RecordFailure); blocks the
// manager flagged as contradicting existing knowledge are conflicts, not
// duplicates.
func dedupEligible(block *Block) bool {
	if block.Label == FailureLabel {
		return false
	}
	return block.Metadata["has_contradictions"] != "true"
}

// autoID reports whether block's ID is derived from its content (empty before
// Add, or the current or legacy formula). Blocks with a caller-chosen ID
// (imports, tier promotion) keep exact-ID semantics only.
func autoID(block *Block) bool {
	return block.ID == "" || block.ID == generateBlockID(block) || block.ID == legacyBlockID(block)
}

// lookupIDs returns the IDs under which an identical block may already be
// stored: the current formula and, when it differs, the legacy one.
func lookupIDs(block *Block) []string {
	cur, old := generateBlockID(block), legacyBlockID(block)
	if cur == old {
		return []string{cur}
	}
	return []string{cur, old}
}

// findDuplicateQ looks up a stored block that block duplicates in its scope
// and tier: first by ID (current and legacy formula), then by normalised
// content, then, unless disabled, by token similarity. It returns nil when
// there is none.
func findDuplicateQ(q rowQuerier, block *Block) (*Block, int64, bool, error) {
	if !autoID(block) {
		return nil, 0, false, nil
	}
	for _, id := range lookupIDs(block) {
		if id == block.ID {
			continue // the caller already looked this one up
		}
		existing, seq, err := getBlockTx(q, id)
		if err != nil {
			return nil, 0, false, err
		}
		if existing != nil && existing.Tier == block.Tier {
			return existing, seq, false, nil
		}
	}
	if !dedupEligible(block) {
		return nil, 0, false, nil
	}
	probe := newDupProbe(block.Content, nearDedupEnabled())
	if probe.norm == "" {
		return nil, 0, false, nil
	}
	rows, err := q.Query(`SELECT id, seq, content, label FROM blocks
		WHERE tier = ? AND scope = ? AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY created_at DESC, id LIMIT ?`,
		string(block.Tier), block.Scope, timeToNS(time.Now()), dedupScanLimit)
	if err != nil {
		return nil, 0, false, fmt.Errorf("scanning for duplicates: %w", err)
	}
	var bestID string
	var bestSeq int64
	var best float64
	var bestSup bool
	for rows.Next() {
		var id, content, label string
		var seq int64
		if err := rows.Scan(&id, &seq, &content, &label); err != nil {
			rows.Close()
			return nil, 0, false, fmt.Errorf("scanning for duplicates: %w", err)
		}
		if label == FailureLabel {
			continue
		}
		if s, sup := probe.score(content); s > best {
			best, bestID, bestSeq, bestSup = s, id, seq, sup
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, false, fmt.Errorf("scanning for duplicates: %w", err)
	}
	if best == 0 {
		return nil, 0, false, nil
	}
	existing, _, err := getBlockTx(q, bestID)
	if err != nil || existing == nil {
		return nil, 0, false, err
	}
	return existing, bestSeq, bestSup, nil
}

// findDuplicateShort is findDuplicateQ for the in-memory short-term tier. It
// returns the index into s.shortTerm, or -1.
func (s *Store) findDuplicateShort(block *Block) (int, bool) {
	if !autoID(block) || !dedupEligible(block) {
		return -1, false
	}
	probe := newDupProbe(block.Content, nearDedupEnabled())
	if probe.norm == "" {
		return -1, false
	}
	best, bestIdx, bestSup := 0.0, -1, false
	for i := range s.shortTerm {
		c := &s.shortTerm[i]
		if c.Tier != TierShort || c.Scope != block.Scope || c.Label == FailureLabel || c.IsExpired() {
			continue
		}
		if sc, sup := probe.score(c.Content); sc > best {
			best, bestIdx, bestSup = sc, i, sup
		}
	}
	return bestIdx, bestSup
}

// FindDuplicate reports the stored block that Add would merge block into, or
// nil. It is read-only, for callers that must predict whether a write adds a
// block (sleep-time dry runs).
func (s *Store) FindDuplicate(block *Block) (*Block, error) {
	probe := *block
	if probe.ID == "" {
		probe.ID = generateBlockID(&probe)
	}
	if probe.Tier == TierShort {
		for i := range s.shortTerm {
			if s.shortTerm[i].ID == probe.ID && s.shortTerm[i].Tier == TierShort {
				b := s.shortTerm[i]
				return &b, nil
			}
		}
		if i, _ := s.findDuplicateShort(&probe); i >= 0 {
			b := s.shortTerm[i]
			return &b, nil
		}
		return nil, nil
	}
	existing, _, err := getBlockTx(s.reader, probe.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Tier == probe.Tier {
		return existing, nil
	}
	existing, _, _, err = findDuplicateQ(s.reader, &probe)
	return existing, err
}
