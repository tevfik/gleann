package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// DefaultMaxContextTokens is the default cap on the memory injected into a
// prompt or a compiled context window, in tokens. It is deliberately generous:
// a typical memory set stays well below it and is rendered unchanged.
// GLEANN_MEMORY_MAX_TOKENS overrides it; 0 disables the cap.
const DefaultMaxContextTokens = 4000

// BlockBudget is the token accounting of a single memory block.
type BlockBudget struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Tier   Tier   `json:"tier"`
	Tokens int    `json:"tokens"`
}

// ContextBudgetReport records what a ContextWindow injected and what the token
// budget forced it to drop. It is attached to a built ContextWindow and is
// serialised in the JSON of `gleann memory context --json` and the REST
// /api/blocks/context response.
type ContextBudgetReport struct {
	MaxTokens     int           `json:"max_tokens"` // 0 = unlimited
	UsedTokens    int           `json:"used_tokens"`
	Tokenizer     string        `json:"tokenizer"`
	InjectedCount int           `json:"injected_count"`
	DroppedCount  int           `json:"dropped_count"`
	Injected      []BlockBudget `json:"injected,omitempty"`
	Dropped       []BlockBudget `json:"dropped,omitempty"`
}

// Summary returns a one-line human-readable account of the budget outcome.
func (r *ContextBudgetReport) Summary() string {
	if r == nil {
		return ""
	}
	limit := "unlimited"
	if r.MaxTokens > 0 {
		limit = fmt.Sprintf("%d", r.MaxTokens)
	}
	s := fmt.Sprintf("memory budget: injected %d block(s), %d/%s tokens; dropped %d block(s)",
		r.InjectedCount, r.UsedTokens, limit, r.DroppedCount)
	if r.DroppedCount > 0 {
		labels := make([]string, 0, len(r.Dropped))
		for _, b := range r.Dropped {
			label := b.Label
			if label == "" {
				label = b.ID
			}
			labels = append(labels, fmt.Sprintf("[%s]%s", b.Tier, label))
		}
		s += " (" + strings.Join(labels, ", ") + ")"
	}
	return s
}

// ApplyBudget trims the window to at most maxTokens estimated tokens and
// records the outcome in Budget.
//
// Blocks are dropped whole, never truncated. The lowest-priority blocks go
// first:
//
//  1. tier: short-term, then medium-term, then long-term (long-term facts are
//     the most durable and are kept longest);
//  2. recency: within a tier the block whose UpdatedAt/LastAccessedAt is older
//     is dropped first;
//  3. access: among equally recent blocks the one with fewer AccessCount is
//     dropped first.
//
// Conversation summaries are bounded (the last five) and are kept as fixed
// overhead; the token estimate covers the whole rendered window, so a window
// that is over budget only because of summaries is reported, but not cut.
// maxTokens <= 0 disables the cap.
func (cw *ContextWindow) ApplyBudget(maxTokens int) *ContextBudgetReport {
	report := &ContextBudgetReport{
		MaxTokens: maxTokens,
		Tokenizer: gleann.DefaultTokenizerName,
	}

	if maxTokens > 0 {
		if total := gleann.EstimateTokens(cw.Render()); total > maxTokens {
			// Decide the drops from per-block estimates in one pass, then
			// render once: re-rendering after every drop is quadratic and
			// took seconds per prompt at a few thousand blocks.
			drop := cw.dropOrder()
			dropped := make(map[blockRef]bool)
			for _, ref := range drop {
				if total <= maxTokens {
					break
				}
				total -= gleann.EstimateTokens(renderBlockLine(cw.blockAt(ref)))
				dropped[ref] = true
			}
			report.Dropped = cw.removeBlocks(dropped)
			// The per-block estimates ignore the section headers; finish
			// exactly, one block at a time (a handful at most).
			for gleann.EstimateTokens(cw.Render()) > maxTokens {
				which, idx, ok := cw.lowestPriorityBlock()
				if !ok {
					break // only the fixed overhead (summaries, tags) remains.
				}
				report.Dropped = append(report.Dropped, blockBudget(cw.removeBlock(which, idx)))
			}
		}
	}
	report.UsedTokens = gleann.EstimateTokens(cw.Render())
	for _, b := range cw.allBlocks() {
		report.Injected = append(report.Injected, blockBudget(b))
	}
	report.InjectedCount = len(report.Injected)
	report.DroppedCount = len(report.Dropped)

	cw.Budget = report
	return report
}

func blockBudget(b Block) BlockBudget {
	return BlockBudget{
		ID:     b.ID,
		Label:  b.Label,
		Tier:   b.Tier,
		Tokens: gleann.EstimateTokens(renderBlockLine(b)),
	}
}

// allBlocks returns every block in render order: long, failures, medium, short.
func (cw *ContextWindow) allBlocks() []Block {
	out := make([]Block, 0, len(cw.LongTerm)+len(cw.Failures)+len(cw.MediumTerm)+len(cw.ShortTerm))
	out = append(out, cw.LongTerm...)
	out = append(out, cw.Failures...)
	out = append(out, cw.MediumTerm...)
	out = append(out, cw.ShortTerm...)
	return out
}

const (
	tierShortIdx = iota
	tierMediumIdx
	tierLongIdx
	tierFailIdx
)

func (cw *ContextWindow) tierSlice(which int) *[]Block {
	switch which {
	case tierShortIdx:
		return &cw.ShortTerm
	case tierMediumIdx:
		return &cw.MediumTerm
	case tierFailIdx:
		return &cw.Failures
	default:
		return &cw.LongTerm
	}
}

// lowestPriorityBlock returns the slice and index of the block to drop next.
func (cw *ContextWindow) lowestPriorityBlock() (which, idx int, ok bool) {
	found := false
	var lowest Block
	for w := tierShortIdx; w <= tierFailIdx; w++ {
		for i, b := range *cw.tierSlice(w) {
			if !found || dropBefore(b, lowest) {
				which, idx, lowest, found = w, i, b, true
			}
		}
	}
	return which, idx, found
}

func (cw *ContextWindow) removeBlock(which, idx int) Block {
	s := cw.tierSlice(which)
	b := (*s)[idx]
	*s = append((*s)[:idx], (*s)[idx+1:]...)
	return b
}

// blockRef addresses a block by tier slice and index.
type blockRef struct{ which, idx int }

func (cw *ContextWindow) blockAt(r blockRef) Block { return (*cw.tierSlice(r.which))[r.idx] }

// dropOrder returns every block, lowest priority (first to drop) first.
func (cw *ContextWindow) dropOrder() []blockRef {
	var refs []blockRef
	for w := tierShortIdx; w <= tierFailIdx; w++ {
		for i := range *cw.tierSlice(w) {
			refs = append(refs, blockRef{w, i})
		}
	}
	sort.SliceStable(refs, func(i, j int) bool { return dropBefore(cw.blockAt(refs[i]), cw.blockAt(refs[j])) })
	return refs
}

// removeBlocks removes the marked blocks, keeping the others in order, and
// returns the budget entries of the removed ones in drop order.
func (cw *ContextWindow) removeBlocks(marked map[blockRef]bool) []BlockBudget {
	var out []BlockBudget
	for _, ref := range cw.dropOrder() {
		if marked[ref] {
			out = append(out, blockBudget(cw.blockAt(ref)))
		}
	}
	for w := tierShortIdx; w <= tierFailIdx; w++ {
		s := cw.tierSlice(w)
		kept := (*s)[:0]
		for i, b := range *s {
			if !marked[blockRef{w, i}] {
				kept = append(kept, b)
			}
		}
		*s = kept
	}
	return out
}

// dropBefore reports whether a must be dropped before b.
func dropBefore(a, b Block) bool {
	if ra, rb := tierRank(a.Tier), tierRank(b.Tier); ra != rb {
		return ra < rb
	}
	ta, tb := effectiveTime(a), effectiveTime(b)
	if !ta.Equal(tb) {
		return ta.Before(tb)
	}
	return a.AccessCount < b.AccessCount
}

func tierRank(t Tier) int {
	switch t {
	case TierShort:
		return 0
	case TierMedium:
		return 1
	case TierLong:
		return 2
	default:
		return 1
	}
}

func effectiveTime(b Block) time.Time {
	if b.LastAccessedAt != nil && b.LastAccessedAt.After(b.UpdatedAt) {
		return *b.LastAccessedAt
	}
	return b.UpdatedAt
}
