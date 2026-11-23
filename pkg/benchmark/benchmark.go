// Package benchmark provides evaluation suites and metrics for code retrieval,
// modeled after SWE-Bench and ContextBench benchmarks.
//
// It evaluates retrieval quality across BM25, Vector (DiskANN/HNSW), Hybrid,
// and GraphRAG strategies, measuring Recall@K, Context Coverage, Token Savings,
// and Query Latency.
package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Task represents a single SWE-bench style retrieval task.
type Task struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Query       string   `json:"query"`
	GoldFiles   []string `json:"gold_files"`
}

// StrategyResult holds metrics for a single retrieval strategy on a task.
type StrategyResult struct {
	Strategy string        `json:"strategy"`
	Recall1  float64       `json:"recall_1"`
	Recall5  float64       `json:"recall_5"`
	Recall10 float64       `json:"recall_10"`
	MRR      float64       `json:"mrr"`
	Coverage float64       `json:"coverage"`
	Tokens   int           `json:"tokens"`
	Latency  time.Duration `json:"latency"`
}

// SummaryResult holds aggregated macro-averaged metrics across all tasks.
type SummaryResult struct {
	Strategy     string  `json:"strategy"`
	AvgRecall1   float64 `json:"avg_recall_1"`
	AvgRecall5   float64 `json:"avg_recall_5"`
	AvgRecall10  float64 `json:"avg_recall_10"`
	AvgMRR       float64 `json:"avg_mrr"`
	AvgCoverage  float64 `json:"avg_coverage"`
	AvgTokens    int     `json:"avg_tokens"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	TokenSavings float64 `json:"token_savings_pct"`
	RecallGainX  float64 `json:"recall_gain_vs_bm25"`
	Unavailable  string  `json:"unavailable,omitempty"`
}

// SearcherFunc abstracts the retrieval call for a strategy.
// Given a query and topK, returns a list of matched file paths or document identifiers.
type SearcherFunc func(ctx context.Context, query string, topK int) ([]string, int, error)

// Runner executes benchmark tasks across configured strategies.
type Runner struct {
	tasks       []Task
	strategies  map[string]SearcherFunc
	unavailable map[string]string
	records     []PerTaskRecord
	ignore      func(path string) bool
	ignored     int
	PerTaskFile string
}

// IgnorePaths makes the runner drop result paths for which fn is true before
// it scores a task, so they take no rank. Use it for the file that holds the
// tasks: when that file is part of the index it contains every query verbatim
// and outranks the gold files, which pushes all strategies down (the lexical
// ones most: BM25 Recall@1 was 0 % on an index that held its own task file).
func (r *Runner) IgnorePaths(fn func(path string) bool) { r.ignore = fn }

// Ignored returns how many result paths IgnorePaths dropped in the last Run.
func (r *Runner) Ignored() int { return r.ignored }

// TaskFileMatcher returns an IgnorePaths function that matches result paths
// naming tasksFile: the same last two path elements (e.g. "tasks/gleann.json"),
// whatever directory the index was built from.
func TaskFileMatcher(tasksFile string) func(string) bool {
	norm := normalizePath(filepath.ToSlash(filepath.Clean(tasksFile)))
	parts := strings.Split(norm, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	tail := strings.Join(parts, "/")
	return func(p string) bool {
		n := normalizePath(p)
		return tail != "" && (n == tail || strings.HasSuffix(n, "/"+tail))
	}
}

// NewRunner creates a new benchmark runner with given tasks.
func NewRunner(tasks []Task) *Runner {
	return &Runner{
		tasks:       tasks,
		strategies:  make(map[string]SearcherFunc),
		unavailable: make(map[string]string),
	}
}

// RegisterStrategy registers a retrieval strategy.
func (r *Runner) RegisterStrategy(name string, fn SearcherFunc) {
	r.strategies[name] = fn
}

// StrategyNames returns the names registered in this run, in name order. A
// strategy that turned out unavailable keeps its name (and row).
func (r *Runner) StrategyNames() []string {
	out := make([]string, 0, len(r.strategies)+len(r.unavailable))
	for name := range r.strategies {
		out = append(out, name)
	}
	for name := range r.unavailable {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RegisterUnavailable marks a strategy as unavailable (e.g. missing binary)
// without causing benchmark runs to abort.
func (r *Runner) RegisterUnavailable(name, reason string) {
	r.unavailable[name] = reason
}

// TaskRecords returns per-task evaluation records collected during the last run.
func (r *Runner) TaskRecords() []PerTaskRecord {
	return r.records
}

// Compare computes paired statistics between two strategies executed in this run.
func (r *Runner) Compare(baseName, candName string) (*ComparisonResult, error) {
	var baseRecs, candRecs []PerTaskRecord
	for _, rec := range r.records {
		if rec.Strategy == baseName {
			baseRecs = append(baseRecs, rec)
		} else if rec.Strategy == candName {
			candRecs = append(candRecs, rec)
		}
	}
	if len(baseRecs) == 0 {
		return nil, fmt.Errorf("strategy %q not found in run results", baseName)
	}
	if len(candRecs) == 0 {
		return nil, fmt.Errorf("strategy %q not found in run results", candName)
	}
	return CompareRecords(baseRecs, candRecs, baseName, candName)
}

// Run executes all tasks across all registered strategies and computes metrics.
func (r *Runner) Run(ctx context.Context, topK int) (map[string]SummaryResult, error) {
	if len(r.tasks) == 0 {
		return nil, fmt.Errorf("no benchmark tasks provided")
	}
	if len(r.strategies) == 0 && len(r.unavailable) == 0 {
		return nil, fmt.Errorf("no retrieval strategies registered")
	}

	strategyResults := make(map[string][]StrategyResult)
	r.ignored = 0
	r.records = nil

	// Write one JSON line per (task, strategy) to PerTaskFile (or GLEANN_BENCH_PERTASK)
	// so strategies can be compared with paired significance tests.
	var perTask *json.Encoder
	perTaskPath := r.PerTaskFile
	if perTaskPath == "" {
		perTaskPath = os.Getenv("GLEANN_BENCH_PERTASK")
	}
	if perTaskPath != "" {
		f, err := os.Create(perTaskPath)
		if err != nil {
			return nil, fmt.Errorf("creating per-task file %q: %w", perTaskPath, err)
		}
		defer f.Close()
		perTask = json.NewEncoder(f)
	}

	for _, task := range r.tasks {
		for name, searcher := range r.strategies {
			if _, unavail := r.unavailable[name]; unavail {
				continue
			}
			start := time.Now()
			foundPaths, tokens, err := searcher(ctx, task.Query, topK)
			latency := time.Since(start)
			if err != nil {
				if errors.Is(err, ErrStrategyUnavailable) {
					r.unavailable[name] = err.Error()
					delete(r.strategies, name)
					continue
				}
				return nil, fmt.Errorf("strategy %s failed on task %s: %w", name, task.ID, err)
			}

			if r.ignore != nil {
				kept := foundPaths[:0:0]
				for _, p := range foundPaths {
					if r.ignore(p) {
						r.ignored++
						continue
					}
					kept = append(kept, p)
				}
				foundPaths = kept
			}

			res := evaluateTask(task, foundPaths, tokens, latency)
			res.Strategy = name
			strategyResults[name] = append(strategyResults[name], res)

			rec := PerTaskRecord{
				Task:         task.ID,
				Strategy:     name,
				R1:           res.Recall1,
				R5:           res.Recall5,
				R10:          res.Recall10,
				MRR:          res.MRR,
				Tokens:       res.Tokens,
				LatencyMs:    float64(res.Latency.Microseconds()) / 1000,
				HasR1:        true,
				HasR5:        true,
				HasR10:       true,
				HasMRR:       true,
				HasTokens:    true,
				HasLatencyMs: true,
			}
			r.records = append(r.records, rec)
			if perTask != nil {
				if err := perTask.Encode(rec); err != nil {
					return nil, fmt.Errorf("writing per-task file %q: %w", perTaskPath, err)
				}
			}
		}
	}

	// Compute summaries
	summaries := make(map[string]SummaryResult)
	for name, results := range strategyResults {
		var (
			sumR1, sumR5, sumR10, sumMRR, sumCov, sumLatMs float64
			sumTok                                         int
		)
		n := float64(len(results))
		for _, res := range results {
			sumR1 += res.Recall1
			sumR5 += res.Recall5
			sumR10 += res.Recall10
			sumMRR += res.MRR
			sumCov += res.Coverage
			sumTok += res.Tokens
			sumLatMs += float64(res.Latency.Microseconds()) / 1000.0
		}

		summaries[name] = SummaryResult{
			Strategy:     name,
			AvgRecall1:   round2(sumR1 / n * 100),
			AvgRecall5:   round2(sumR5 / n * 100),
			AvgRecall10:  round2(sumR10 / n * 100),
			AvgMRR:       round3(sumMRR / n),
			AvgCoverage:  round2(sumCov / n * 100),
			AvgTokens:    int(math.Round(float64(sumTok) / n)),
			AvgLatencyMs: round2(sumLatMs / n),
		}
	}

	for name, reason := range r.unavailable {
		summaries[name] = SummaryResult{
			Strategy:    name,
			Unavailable: reason,
		}
	}

	// Calculate gains against baseline (BM25 if available)
	baseline, hasBaseline := summaries["BM25"]
	if !hasBaseline || baseline.Unavailable != "" {
		// Fall back to first available strategy as baseline
		for _, s := range summaries {
			if s.Unavailable == "" {
				baseline = s
				hasBaseline = true
				break
			}
		}
	}

	for name, s := range summaries {
		if s.Unavailable != "" {
			continue
		}
		if baseline.AvgTokens > 0 {
			tokenDiff := float64(baseline.AvgTokens-s.AvgTokens) / float64(baseline.AvgTokens) * 100
			s.TokenSavings = round2(tokenDiff)
		}
		if baseline.AvgRecall5 > 0 {
			s.RecallGainX = round2(s.AvgRecall5 / baseline.AvgRecall5)
		}
		summaries[name] = s
	}

	return summaries, nil
}

// evaluateTask calculates recall, coverage, and reciprocal rank for a single task.
//
// Retrieval returns chunks, so several results can come from the same file.
// Ranks are file-level: only the first occurrence of a file counts, and recall@k
// is the share of distinct gold files found within the first k distinct files.
func evaluateTask(task Task, foundPaths []string, tokens int, latency time.Duration) StrategyResult {
	goldSet := make(map[string]bool)
	for _, g := range task.GoldFiles {
		goldSet[normalizePath(g)] = true
	}

	var (
		firstRank   = 0
		coveredAt   = make(map[string]int) // gold file -> rank (1-based) of its first hit
		seen        = make(map[string]bool)
		distinctIdx = 0
	)

	for _, p := range foundPaths {
		norm := normalizePath(p)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		distinctIdx++

		for g := range goldSet {
			if _, done := coveredAt[g]; done {
				continue
			}
			if strings.HasSuffix(norm, g) || strings.HasSuffix(g, norm) {
				coveredAt[g] = distinctIdx
				if firstRank == 0 {
					firstRank = distinctIdx
				}
				break
			}
		}
	}

	var mrr float64
	if firstRank > 0 {
		mrr = 1.0 / float64(firstRank)
	}

	totalGold := len(goldSet)
	if totalGold == 0 {
		totalGold = 1
	}

	within := func(k int) float64 {
		n := 0
		for _, rank := range coveredAt {
			if rank <= k {
				n++
			}
		}
		return float64(n) / float64(totalGold)
	}

	return StrategyResult{
		Recall1:  within(1),
		Recall5:  within(5),
		Recall10: within(10),
		MRR:      mrr,
		Coverage: float64(len(coveredAt)) / float64(totalGold),
		Tokens:   tokens,
		Latency:  latency,
	}
}

func normalizePath(p string) string {
	p = strings.TrimSpace(strings.ToLower(p))
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// RenderMarkdownReport formats summaries into a GitHub-flavored Markdown report.
func RenderMarkdownReport(summaries map[string]SummaryResult, taskCount int, comparisons ...*ComparisonResult) string {
	var sb strings.Builder
	sb.WriteString("# Retrieval benchmark report\n\n")
	sb.WriteString(fmt.Sprintf("> **%d tasks** in the ContextBench / SWE-Bench task shape (a query and the files that answer it), run against one index. Recall@K: the share of a task's gold files among the first K distinct files returned, averaged over the tasks.\n\n", taskCount))

	sb.WriteString("| Strategy | Recall@1 | Recall@5 | Recall@10 | MRR | Coverage | Avg Tokens | Tokens vs BM25 | Latency |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |\n")

	// Token Savings is the reduction of context tokens against BM25: a
	// positive value (bold, shown with a minus) means fewer tokens.
	row := func(s SummaryResult) {
		if s.Unavailable != "" {
			sb.WriteString(fmt.Sprintf("| **%s** | *(%s)* | — | — | — | — | — | — | — |\n",
				s.Strategy, s.Unavailable))
			return
		}
		savings := "0.0%"
		switch {
		case s.TokenSavings > 0:
			savings = fmt.Sprintf("**-%.1f%%**", s.TokenSavings)
		case s.TokenSavings < 0:
			savings = fmt.Sprintf("+%.1f%%", -s.TokenSavings) // more tokens than BM25
		}
		sb.WriteString(fmt.Sprintf("| **%s** | %.1f%% | %.1f%% | %.1f%% | %.3f | %.1f%% | %d | %s | %.1fms |\n",
			s.Strategy, s.AvgRecall1, s.AvgRecall5, s.AvgRecall10, s.AvgMRR, s.AvgCoverage, s.AvgTokens, savings, s.AvgLatencyMs,
		))
	}

	// Order strategies predictably: BM25 first, then the vector backends, the
	// bare "Hybrid" of older runs, the configured default (what
	// `gleann search` does), GraphRAG, and finally the other hybrid variants
	// and baselines, the α variants by weight rather than by the byte order
	// of "α".
	names := make([]string, 0, len(summaries))
	for name := range summaries {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		ri, ai := strategyRank(names[i])
		rj, aj := strategyRank(names[j])
		if ri != rj {
			return ri < rj
		}
		if ai != aj {
			return ai < aj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		row(summaries[name])
	}

	sb.WriteString("\n### Reading the table\n\n")
	// The best strategy per metric, whichever it is: the report states what
	// was measured and does not pick the metric that favours one strategy.
	for _, m := range []struct {
		name string
		get  func(SummaryResult) float64
		unit string
	}{
		{"Recall@5", func(s SummaryResult) float64 { return s.AvgRecall5 }, "%"},
		{"Recall@10", func(s SummaryResult) float64 { return s.AvgRecall10 }, "%"},
		{"MRR", func(s SummaryResult) float64 { return s.AvgMRR }, ""},
	} {
		best, names := bestStrategies(summaries, m.get)
		if len(names) == 0 || best <= 0 {
			continue
		}
		val := fmt.Sprintf("%.1f%%", best)
		if m.unit == "" {
			val = fmt.Sprintf("%.3f", best)
		}
		sb.WriteString(fmt.Sprintf("- Best %s: %s (%s).\n", m.name, strings.Join(names, ", "), val))
	}
	if gr, ok := summaries["GraphRAG"]; ok {
		if name, hy, ok := graphRAGComparison(summaries); ok && gr.AvgRecall1 == hy.AvgRecall1 && gr.AvgRecall5 == hy.AvgRecall5 &&
			gr.AvgRecall10 == hy.AvgRecall10 && gr.AvgMRR == hy.AvgMRR {
			sb.WriteString(fmt.Sprintf("- GraphRAG and %s have the same recall and MRR: the graph expansion did not change the rank of a gold file in these tasks.\n", name))
		}
	}
	if rg, ok := summaries["Ripgrep"]; ok {
		if rg.Unavailable != "" {
			sb.WriteString(fmt.Sprintf("- Ripgrep baseline: %s.\n", rg.Unavailable))
		} else {
			sb.WriteString("- Ripgrep baseline: runs `rg -F -c -i` for non-stopword query terms in the index source directory, ranks files by matching-line count (`rg -c`), and counts tokens of retrieved files.\n")
		}
	}
	if taskCount > 0 && taskCount < 200 {
		sb.WriteString(fmt.Sprintf("- %d tasks: one task weighs %.1f points, so a difference of one or two tasks is not significant.\n",
			taskCount, 100/float64(taskCount)))
	}

	for _, comp := range comparisons {
		if comp != nil {
			sb.WriteString("\n" + comp.FormatMarkdown())
		}
	}

	return sb.String()
}

// strategyRank places a strategy in the report's row order and returns the
// weight that orders the hybrid variants. Buckets: BM25, the vector backends,
// a bare "Hybrid" (the name older runs and per-task files use), the configured
// default hybrid row, GraphRAG, then any other hybrid row (one without a
// weight sorts first), then the remaining strategies.
func strategyRank(name string) (bucket int, alpha float64) {
	switch name {
	case "BM25":
		return 0, 0
	case "Vector (DiskANN+PQ)":
		return 1, 0
	case "Vector (HNSW)":
		return 2, 0
	case "Vector (FAISS)":
		return 3, 0
	case "Hybrid":
		return 4, 0
	}
	if strings.HasPrefix(name, "Hybrid (default") {
		return 5, 0
	}
	if name == "GraphRAG" {
		return 6, 0
	}
	if strings.HasPrefix(name, "Hybrid") {
		if a, ok := hybridAlphaFromLabel(name); ok {
			return 7, a
		}
		return 7, -1
	}
	return 8, 0
}

// hybridAlphaFromLabel parses the weight in "Hybrid (α=0.5)" or
// "Hybrid (default, α=0.5)".
func hybridAlphaFromLabel(name string) (float64, bool) {
	const marker = "α="
	i := strings.LastIndex(name, marker)
	if i < 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(name[i+len(marker):], ")"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// graphRAGComparison returns the hybrid row that SearchGraphRAG is comparable
// with: it seeds retrieval with a fixed α=0.5, so the note must name the row
// that measured the same weight, not the configured default. Older reports and
// per-task files call that row "Hybrid".
func graphRAGComparison(summaries map[string]SummaryResult) (string, SummaryResult, bool) {
	for _, name := range []string{"Hybrid (α=0.5)", "Hybrid (default, α=0.5)", "Hybrid"} {
		if s, ok := summaries[name]; ok {
			return name, s, true
		}
	}
	// A run that named its rows differently: use any α=0.5 hybrid row.
	var names []string
	for name := range summaries {
		if a, ok := hybridAlphaFromLabel(name); ok && a == 0.5 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0], summaries[names[0]], true
	}
	return "", SummaryResult{}, false
}

// bestStrategies returns the highest value of a metric and the strategies that
// reach it, in name order.
func bestStrategies(summaries map[string]SummaryResult, get func(SummaryResult) float64) (float64, []string) {
	var best float64
	var names []string
	for name, s := range summaries {
		if s.Unavailable != "" {
			continue
		}
		switch v := get(s); {
		case v > best:
			best, names = v, []string{name}
		case v == best && v > 0:
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return best, names
}

// RenderJSON returns the summaries serialized as indented JSON.
func RenderJSON(summaries map[string]SummaryResult) (string, error) {
	b, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
