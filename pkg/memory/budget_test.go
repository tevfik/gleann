package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

func budgetTestBlock(tier Tier, label string, updated time.Time, access int) Block {
	return Block{
		ID:          label,
		Tier:        tier,
		Label:       label,
		Content:     strings.Repeat("x", 400),
		CreatedAt:   updated,
		UpdatedAt:   updated,
		AccessCount: access,
	}
}

func droppedLabels(r *ContextBudgetReport) []string {
	out := make([]string, 0, len(r.Dropped))
	for _, b := range r.Dropped {
		out = append(out, b.Label)
	}
	return out
}

// Under the cap the rendered window must be byte-identical to today's output.
func TestContextWindowApplyBudgetUnderBudgetUnchanged(t *testing.T) {
	now := time.Now()
	cw := &ContextWindow{
		LongTerm:   []Block{budgetTestBlock(TierLong, "long", now, 2)},
		MediumTerm: []Block{budgetTestBlock(TierMedium, "medium", now, 1)},
		ShortTerm:  []Block{budgetTestBlock(TierShort, "short", now, 0)},
		Summaries:  []Summary{{Title: "summary", Content: "earlier talk", CreatedAt: now}},
	}
	before := cw.Render()

	report := cw.ApplyBudget(100000)
	after := cw.Render()

	if before != after {
		t.Fatalf("under budget the render changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if report.DroppedCount != 0 {
		t.Fatalf("dropped %d block(s) under the cap", report.DroppedCount)
	}
	if report.InjectedCount != 3 {
		t.Fatalf("InjectedCount = %d, want 3", report.InjectedCount)
	}
	if report.UsedTokens != gleann.EstimateTokens(before) {
		t.Fatalf("UsedTokens = %d, want %d", report.UsedTokens, gleann.EstimateTokens(before))
	}
}

// Over budget the lowest-priority blocks go first: tier, then recency, then
// access count. A tiny cap forces every block out in that order.
func TestContextWindowApplyBudgetDropsInPriorityOrder(t *testing.T) {
	now := time.Now()
	cw := &ContextWindow{
		LongTerm:   []Block{budgetTestBlock(TierLong, "long", now, 9)},
		MediumTerm: []Block{budgetTestBlock(TierMedium, "medium", now, 9)},
		ShortTerm:  []Block{budgetTestBlock(TierShort, "short", now, 9)},
	}

	report := cw.ApplyBudget(1)

	want := []string{"short", "medium", "long"}
	got := droppedLabels(report)
	if len(got) != len(want) {
		t.Fatalf("dropped %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dropped %v, want %v", got, want)
		}
	}
	if report.InjectedCount != 0 {
		t.Fatalf("InjectedCount = %d, want 0", report.InjectedCount)
	}
}

func TestContextWindowApplyBudgetRecencyAndAccess(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now()

	t.Run("older block drops first", func(t *testing.T) {
		cw := &ContextWindow{MediumTerm: []Block{
			budgetTestBlock(TierMedium, "old", old, 0),
			budgetTestBlock(TierMedium, "recent", recent, 0),
		}}
		report := cw.ApplyBudget(1)
		if got := droppedLabels(report); len(got) != 2 || got[0] != "old" {
			t.Fatalf("drop order = %v, want old first", got)
		}
	})

	t.Run("less accessed block drops first at equal recency", func(t *testing.T) {
		cw := &ContextWindow{MediumTerm: []Block{
			budgetTestBlock(TierMedium, "rare", recent, 0),
			budgetTestBlock(TierMedium, "hot", recent, 5),
		}}
		report := cw.ApplyBudget(1)
		if got := droppedLabels(report); len(got) != 2 || got[0] != "rare" {
			t.Fatalf("drop order = %v, want rare first", got)
		}
	})
}

func TestContextWindowApplyBudgetReportNumbers(t *testing.T) {
	now := time.Now()
	blocks := []Block{
		budgetTestBlock(TierLong, "long", now, 0),
		budgetTestBlock(TierMedium, "medium", now, 0),
		budgetTestBlock(TierShort, "short", now, 0),
	}
	cw := &ContextWindow{
		LongTerm:   blocks[0:1],
		MediumTerm: blocks[1:2],
		ShortTerm:  blocks[2:3],
	}
	byLabel := map[string]Block{}
	for _, b := range blocks {
		byLabel[b.Label] = b
	}

	report := cw.ApplyBudget(1)

	if report.MaxTokens != 1 {
		t.Fatalf("MaxTokens = %d, want 1", report.MaxTokens)
	}
	if report.Tokenizer == "" {
		t.Fatal("Tokenizer is empty")
	}
	if report.InjectedCount+report.DroppedCount != len(blocks) {
		t.Fatalf("injected %d + dropped %d != %d blocks",
			report.InjectedCount, report.DroppedCount, len(blocks))
	}
	if report.UsedTokens != gleann.EstimateTokens(cw.Render()) {
		t.Fatalf("UsedTokens = %d, want %d", report.UsedTokens, gleann.EstimateTokens(cw.Render()))
	}
	for _, d := range report.Dropped {
		want := gleann.EstimateTokens(renderBlockLine(byLabel[d.Label]))
		if d.Tokens != want {
			t.Fatalf("Dropped[%s].Tokens = %d, want %d", d.Label, d.Tokens, want)
		}
	}
}

func TestManagerBuildContextBudget(t *testing.T) {
	open := func(t *testing.T) *Manager {
		t.Helper()
		store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return NewManager(store)
	}

	t.Run("default and unlimited", func(t *testing.T) {
		t.Setenv("GLEANN_MEMORY_MAX_TOKENS", "")
		if got := open(t).MaxContextTokens; got != DefaultMaxContextTokens {
			t.Fatalf("default MaxContextTokens = %d, want %d", got, DefaultMaxContextTokens)
		}
		t.Setenv("GLEANN_MEMORY_MAX_TOKENS", "0")
		if got := open(t).MaxContextTokens; got != 0 {
			t.Fatalf("MaxContextTokens = %d, want 0 (unlimited)", got)
		}
	})

	t.Run("env budget drops and reports", func(t *testing.T) {
		t.Setenv("GLEANN_MEMORY_MAX_TOKENS", "1")
		mgr := open(t)
		if _, err := mgr.AddNote(TierLong, "fact", strings.Repeat("y", 400)); err != nil {
			t.Fatal(err)
		}
		cw, err := mgr.BuildContext()
		if err != nil {
			t.Fatal(err)
		}
		if cw.Budget == nil || cw.Budget.DroppedCount != 1 {
			t.Fatalf("Budget = %+v, want 1 dropped block", cw.Budget)
		}
		if cw.Render() != "" {
			t.Fatalf("over-budget render should be empty, got %q", cw.Render())
		}
	})

	t.Run("under budget keeps today's output", func(t *testing.T) {
		t.Setenv("GLEANN_MEMORY_MAX_TOKENS", "100000")
		mgr := open(t)
		if _, err := mgr.AddNote(TierLong, "fact", "short fact"); err != nil {
			t.Fatal(err)
		}
		raw, err := mgr.Store().BuildContext()
		if err != nil {
			t.Fatal(err)
		}
		cw, err := mgr.BuildContext()
		if err != nil {
			t.Fatal(err)
		}
		if cw.Render() != raw.Render() {
			t.Fatalf("budgeted render differs:\nbudgeted:\n%s\nraw:\n%s", cw.Render(), raw.Render())
		}
		if cw.Budget == nil || cw.Budget.DroppedCount != 0 {
			t.Fatalf("Budget = %+v, want nothing dropped", cw.Budget)
		}
	})
}

// The budget must stay cheap with thousands of blocks: dropping one block at
// a time with a full re-render was quadratic (about 15 s at 5,000 blocks).
func TestApplyBudgetScalesLinearly(t *testing.T) {
	cw := &ContextWindow{}
	for i := 0; i < 5000; i++ {
		cw.ShortTerm = append(cw.ShortTerm, Block{ID: fmt.Sprint(i), Tier: TierShort, Label: "l",
			Content: strings.Repeat("word ", 40), UpdatedAt: time.Unix(int64(i), 0)})
	}
	start := time.Now()
	r := cw.ApplyBudget(4000)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("ApplyBudget on 5,000 blocks took %s", took)
	}
	if r.UsedTokens > 4000 || r.DroppedCount == 0 || r.InjectedCount+r.DroppedCount != 5000 {
		t.Fatalf("report = used %d, injected %d, dropped %d", r.UsedTokens, r.InjectedCount, r.DroppedCount)
	}
	// The newest blocks survive: the oldest short-term blocks go first.
	if got := cw.ShortTerm[len(cw.ShortTerm)-1].ID; got != "4999" {
		t.Errorf("last kept block = %s, want 4999", got)
	}
}
