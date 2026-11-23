package gleann

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tevfik/gleann/internal/store"
)

// filterIndexMaxIn bounds the list of an `in` filter the filter indexes
// answer; a longer list goes to the pass over the table.
const filterIndexMaxIn = 256

// indexedFilter is one metadata filter compiled for the filter indexes of
// the record store (store.SelectByMetaIndex): the index ranges that together
// hold every passage the filter engine would match, and the meta keys whose
// index they read.
type indexedFilter struct {
	keys     []string
	branches []store.MetaBranch
}

// compileFilterIndex compiles one filter to index ranges, or reports that the
// filter indexes cannot answer it: then the pass over the table decides
// (compileFilterSQL), as before.
//
// Answerable are eq, in and exists(true) on a field whose resolution
// (resolveMetadataField) is a meta key, the kind/type pair or the source
// column. Every range is either sure (its passages match, whatever the rest
// of their metadata says) or not (the caller verifies them with the filter
// engine), and the ranges together are a superset of the engine's matches.
// What makes a range sure follows from how the engine compares: fmt.Sprint of
// the value json.Unmarshal produced against fmt.Sprint of the filter value.
//
//   - a JSON string prints as itself: text equal to the needle is sure;
//   - true, false and null print as "true", "false" and "<nil>";
//   - a JSON integer prints as its digits when it is not zero and has at most
//     6 of them (%g switches to the exponent form at 1e+06, and "-0" prints
//     as "-0");
//   - any other number is verified, and only when the needle can be a printed
//     number at all (digits, sign, point, exponent);
//   - arrays and objects print as "[...]" and "map[...]": verified, and only
//     for a needle of that shape.
//
// Values SQLite and Go decode differently (a number outside float64, a key
// that occurs twice) have the type "odd" in the filter index and are verified
// for every needle. Strings with bytes that are not UTF-8 are told apart by
// the needle: Go reads such a byte as U+FFFD, so a needle with U+FFFD asks
// for every text value of the key to be verified, and a needle that is not
// UTF-8 can never equal a decoded string and is left out of the text range.
func compileFilterIndex(f MetadataFilter) (indexedFilter, bool) {
	var needles []string
	switch f.Operator {
	case OpEqual:
		needles = []string{fmt.Sprint(f.Value)}
	case OpIn:
		switch v := f.Value.(type) {
		case []any:
			for _, it := range v {
				needles = append(needles, fmt.Sprint(it))
			}
		case []string:
			needles = append(needles, v...)
		default:
			return indexedFilter{}, false
		}
		if len(needles) == 0 || len(needles) > filterIndexMaxIn {
			return indexedFilter{}, false
		}
	case OpExists:
		if want, _ := f.Value.(bool); !want {
			return indexedFilter{}, false // the passages WITHOUT the key are in no index
		}
	default:
		return indexedFilter{}, false
	}
	needles = uniqueStrings(needles)

	switch f.Field {
	case "", "file", "ext", "extension", "chunk_index":
		// file resolves through the source column second, ext through
		// filepath.Ext, chunk_index is a column: not worth index ranges.
		return indexedFilter{}, false
	case "source":
		if f.Operator == OpExists {
			return indexedFilter{}, false // nearly every passage has a source
		}
		// A non-empty source column IS metadata["source"]; only a passage
		// without one resolves through its meta JSON (file, or a source key
		// a foreign writer left there), so those are verified.
		var out indexedFilter
		for _, n := range needles {
			if n != "" {
				out.branches = append(out.branches, store.MetaBranch{Kind: store.MetaBySource, Source: n, Sure: true})
			}
		}
		out.branches = append(out.branches, store.MetaBranch{Kind: store.MetaBySource, Source: ""})
		return out, true
	}
	if !store.MetaIndexable(f.Field) {
		return indexedFilter{}, false
	}
	out := indexedFilter{keys: []string{f.Field}}
	exists := f.Operator == OpExists
	out.branches = keyIndexBranches(f.Field, needles, exists, true)
	// kind and type stand in for each other: the alias decides only when the
	// field itself is absent, so its ranges nominate passages for
	// verification (for exists either key is enough).
	alias := ""
	switch f.Field {
	case "kind":
		alias = "type"
	case "type":
		alias = "kind"
	}
	if alias != "" {
		out.keys = append(out.keys, alias)
		out.branches = append(out.branches, keyIndexBranches(alias, needles, exists, exists)...)
	}
	out.branches = append(out.branches, store.MetaBranch{Kind: store.MetaDamaged})
	return out, true
}

// keyIndexBranches returns the index ranges of one meta key for the needles
// of an eq/in filter, or the whole key for exists. With sure false every
// range is for verification.
func keyIndexBranches(key string, needles []string, exists, sure bool) []store.MetaBranch {
	if exists {
		return []store.MetaBranch{{Kind: store.MetaByKey, Key: key, Sure: sure}}
	}
	var texts, ints []any
	var isTrue, isFalse, isNull, numeric, otherNumber, array, object, replaced bool
	for _, n := range needles {
		switch {
		case !utf8.ValidString(n):
			// no decoded JSON string is invalid UTF-8
		case strings.ContainsRune(n, utf8.RuneError):
			replaced = true
			texts = append(texts, n)
		default:
			texts = append(texts, n)
		}
		switch n {
		case "true":
			isTrue = true
		case "false":
			isFalse = true
		case "<nil>":
			isNull = true
		}
		if v, ok := exactJSONInt(n); ok {
			ints = append(ints, v)
			numeric = true
		} else if printedNumber(n) {
			numeric, otherNumber = true, true
		}
		if strings.HasPrefix(n, "[") && strings.HasSuffix(n, "]") {
			array = true
		}
		if strings.HasPrefix(n, "map[") && strings.HasSuffix(n, "]") {
			object = true
		}
	}
	byType := func(typ string, sure bool, values ...any) store.MetaBranch {
		return store.MetaBranch{Kind: store.MetaByKey, Key: key, Type: typ, Values: values, Sure: sure}
	}
	// byType with no values selects every value of the type, so an empty
	// list must not become a branch.
	var out []store.MetaBranch
	switch {
	case replaced:
		out = append(out, byType("text", false)) // the stored bytes are not what Go decoded
	case len(texts) > 0:
		out = append(out, byType("text", sure, texts...))
	}
	out = append(out, byType(store.MetaOddType, false))
	if isTrue {
		out = append(out, byType("true", sure))
	}
	if isFalse {
		out = append(out, byType("false", sure))
	}
	if isNull {
		out = append(out, byType("null", sure))
	}
	switch {
	case otherNumber:
		out = append(out, byType("integer", false)) // "0", "-0", "1e+06", ...: the engine decides
	case len(ints) > 0:
		out = append(out, byType("integer", sure, ints...))
	}
	if numeric {
		out = append(out, byType("real", false)) // 5.0 prints as "5"
	}
	if array {
		out = append(out, byType("array", false))
	}
	if object {
		out = append(out, byType("object", false))
	}
	return out
}

// exactJSONInt reports whether s is how fmt.Sprint prints exactly one JSON
// integer: an optional minus, no leading zero, at most 6 digits. The engine
// holds a JSON number as float64 and fmt.Sprint prints a float64 with %g:
// plain digits up to 999999, exponent form from 1e+06 on (1234567 prints as
// "1.234567e+06"), so a longer run of digits is not what any integer prints
// as. Zero is left out: "-0" is a JSON integer that Go prints as "-0" and
// SQLite reads as 0.
func exactJSONInt(s string) (int64, bool) {
	digits := strings.TrimPrefix(s, "-")
	if len(digits) == 0 || len(digits) > 6 || digits[0] < '1' || digits[0] > '9' {
		return 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// printedNumber reports whether s can be fmt.Sprint of a float64 decoded
// from JSON: digits, sign, decimal point and exponent only.
func printedNumber(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' && c != 'e' {
			return false
		}
	}
	return true
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// preselectOutcome is how an index pre-selection ended.
type preselectOutcome int

const (
	// preselectUnavailable: the filter indexes cannot answer (a filter they
	// do not cover, a missing index that may not be created now, a store
	// error). The pass over the table may still answer.
	preselectUnavailable preselectOutcome = iota
	// preselectDone: the returned set is exact.
	preselectDone
	// preselectNotSelective: the filter provably matches more passages than
	// the limit; no pass over the table is needed to know that.
	preselectNotSelective
)

// indexedPassage is what the filter indexes said about one passage for one
// filter.
type indexedPassage struct {
	slot    *int64
	sure    bool // a sure range selected it
	damaged bool // its meta must be verified whatever the ranges said
	hasMeta bool
	source  string
	meta    string
}

// preselectIndexed answers a metadata filter from the filter indexes of the
// record store, so the cost follows the number of matches instead of the
// number of passages. It maps each matching passage's searcher ID to its lex
// rowid, exactly as the pass over the table does (preselectFilterIDsLimit).
//
// A meta key without an index gets one when create is set (one pass over the
// passages, once per key and index database; see store.EnsureMetaIndexes),
// otherwise the outcome is preselectUnavailable. Each filter is read from its
// own ranges and the sets are combined here: with "and" a passage must be in
// every set, with "or" in any. A passage every (any) set is sure about is a
// match; every other candidate, and every passage of the damaged range, is
// verified by the filter engine against its metadata.
func (s *LeannSearcher) preselectIndexed(ctx context.Context, sp *storePassages, engine *MetadataFilterEngine, limit int, create bool) (map[int64]int64, preselectOutcome) {
	if len(engine.Filters) == 0 {
		return nil, preselectUnavailable
	}
	compiled := make([]indexedFilter, len(engine.Filters))
	var keys []string
	for i, f := range engine.Filters {
		c, ok := compileFilterIndex(f)
		if !ok {
			return nil, preselectUnavailable
		}
		compiled[i] = c
		keys = append(keys, c.keys...)
	}
	if keys = uniqueStrings(keys); len(keys) > 0 {
		have, err := sp.st.MetaIndexes(ctx, keys)
		if err != nil {
			return nil, preselectUnavailable
		}
		for _, k := range keys {
			if have[k] {
				continue
			}
			if !create || sp.st.EnsureMetaIndexes(ctx, keys) != nil {
				return nil, preselectUnavailable
			}
			break
		}
	}

	or := engine.Logic == "or"
	sets := make([]map[int64]*indexedPassage, len(compiled))
	for i, c := range compiled {
		ms, err := sp.st.SelectByMetaIndex(ctx, c.branches, limit+1)
		if err != nil {
			return nil, preselectUnavailable
		}
		set := make(map[int64]*indexedPassage, len(ms))
		sure := 0
		for _, m := range ms {
			p := set[m.ID]
			if p == nil {
				p = &indexedPassage{slot: m.VecSlot}
				set[m.ID] = p
			}
			switch {
			case m.Damaged:
				p.damaged = true
			case m.Sure:
				p.sure = true
			}
			if !m.Sure {
				p.hasMeta, p.source, p.meta = true, m.Source, m.Meta
			}
		}
		for _, p := range set {
			if p.sure && !p.damaged {
				sure++
			}
		}
		if len(ms) > limit {
			// The rows were cut. With one filter, or with "or", more sure
			// passages than the limit prove the filter is not selective;
			// anything else is left to the pass over the table.
			if sure > limit && (or || len(compiled) == 1) {
				return nil, preselectNotSelective
			}
			return nil, preselectUnavailable
		}
		sets[i] = set
	}

	allowed := make(map[int64]int64)
	accept := func(id int64, ps []*indexedPassage, sure bool) {
		var slot *int64
		var withMeta *indexedPassage
		for _, p := range ps {
			slot = p.slot
			if p.damaged {
				sure = false
			}
			if p.hasMeta {
				withMeta = p
			}
		}
		if !sure {
			if withMeta == nil {
				return // unreachable: a passage no set is sure about came with its metadata
			}
			row := store.Passage{ID: id, VecSlot: slot, Source: withMeta.source, Meta: withMeta.meta}
			if !engine.Match(storePassageToGleann(row).Metadata) {
				return
			}
		}
		// The searcher ID, as storePassageToGleann assigns it: the vec_slot,
		// or the negative rowid of a passage without one.
		if slot != nil {
			allowed[*slot] = id
		} else {
			allowed[-id] = id
		}
	}
	if or {
		seen := make(map[int64]bool)
		for _, set := range sets {
			for id := range set {
				if seen[id] {
					continue
				}
				seen[id] = true
				var ps []*indexedPassage
				sure := false
				for _, other := range sets {
					if p := other[id]; p != nil {
						ps = append(ps, p)
						sure = sure || p.sure
					}
				}
				accept(id, ps, sure)
			}
		}
	} else {
		smallest := sets[0]
		for _, set := range sets[1:] {
			if len(set) < len(smallest) {
				smallest = set
			}
		}
		ps := make([]*indexedPassage, 0, len(sets))
	candidates:
		for id := range smallest {
			ps = ps[:0]
			sure := true
			for _, set := range sets {
				p := set[id]
				if p == nil {
					continue candidates
				}
				ps = append(ps, p)
				sure = sure && p.sure
			}
			accept(id, ps, sure)
		}
	}
	if len(allowed) > limit {
		return nil, preselectNotSelective
	}
	return allowed, preselectDone
}

// EnsureFilterIndexes creates the filter indexes of the record store for the
// given filter fields, so that eq, in and exists filters on them are answered
// from an index whatever the size of the index (see preselectIndexed). A
// filtered search creates them on demand on an index of up to
// filterExactScanMax passages; on a larger one the first pass over the table
// is the caller's decision, made by calling this once. It is a no-op for
// fields that already have an index or need none (source), and an error for a
// field the filter indexes cannot serve.
func (s *LeannSearcher) EnsureFilterIndexes(ctx context.Context, fields ...string) error {
	sp, ok := s.passages.(*storePassages)
	if !ok {
		return fmt.Errorf("creating filter indexes: index %q has no format-2 record store", s.meta.Name)
	}
	var keys []string
	for _, f := range fields {
		c, ok := compileFilterIndex(MetadataFilter{Field: f, Operator: OpExists, Value: true})
		if !ok && f != "source" {
			return fmt.Errorf("creating filter indexes: field %q cannot be answered from a filter index", f)
		}
		keys = append(keys, c.keys...)
	}
	if err := sp.st.EnsureMetaIndexes(ctx, uniqueStrings(keys)); err != nil {
		return fmt.Errorf("creating filter indexes of %q: %w", s.meta.Name, err)
	}
	return nil
}
