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
	"fmt"
	"math"
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
	Strategy      string  `json:"strategy"`
	AvgRecall1    float64 `json:"avg_recall_1"`
	AvgRecall5    float64 `json:"avg_recall_5"`
	AvgRecall10   float64 `json:"avg_recall_10"`
	AvgMRR        float64 `json:"avg_mrr"`
	AvgCoverage   float64 `json:"avg_coverage"`
	AvgTokens     int     `json:"avg_tokens"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	TokenSavings  float64 `json:"token_savings_pct"`
	RecallGainX   float64 `json:"recall_gain_vs_bm25"`
}

// SearcherFunc abstracts the retrieval call for a strategy.
// Given a query and topK, returns a list of matched file paths or document identifiers.
type SearcherFunc func(ctx context.Context, query string, topK int) ([]string, int, error)

// Runner executes benchmark tasks across configured strategies.
type Runner struct {
	tasks      []Task
	strategies map[string]SearcherFunc
}

// NewRunner creates a new benchmark runner with given tasks.
func NewRunner(tasks []Task) *Runner {
	return &Runner{
		tasks:      tasks,
		strategies: make(map[string]SearcherFunc),
	}
}

// RegisterStrategy registers a retrieval strategy.
func (r *Runner) RegisterStrategy(name string, fn SearcherFunc) {
	r.strategies[name] = fn
}

// Run executes all tasks across all registered strategies and computes metrics.
func (r *Runner) Run(ctx context.Context, topK int) (map[string]SummaryResult, error) {
	if len(r.tasks) == 0 {
		return nil, fmt.Errorf("no benchmark tasks provided")
	}
	if len(r.strategies) == 0 {
		return nil, fmt.Errorf("no retrieval strategies registered")
	}

	strategyResults := make(map[string][]StrategyResult)

	for _, task := range r.tasks {
		for name, searcher := range r.strategies {
			start := time.Now()
			foundPaths, tokens, err := searcher(ctx, task.Query, topK)
			latency := time.Since(start)
			if err != nil {
				return nil, fmt.Errorf("strategy %s failed on task %s: %w", name, task.ID, err)
			}

			res := evaluateTask(task, foundPaths, tokens, latency)
			res.Strategy = name
			strategyResults[name] = append(strategyResults[name], res)
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

	// Calculate gains against baseline (BM25 if available)
	baseline, hasBaseline := summaries["BM25"]
	if !hasBaseline {
		// Fall back to first available strategy as baseline
		for _, s := range summaries {
			baseline = s
			break
		}
	}

	for name, s := range summaries {
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
func evaluateTask(task Task, foundPaths []string, tokens int, latency time.Duration) StrategyResult {
	goldSet := make(map[string]bool)
	for _, g := range task.GoldFiles {
		goldSet[normalizePath(g)] = true
	}

	var (
		hitsR1, hitsR5, hitsR10 int
		firstRank               = 0
		coveredGold             = make(map[string]bool)
	)

	for i, p := range foundPaths {
		norm := normalizePath(p)
		matched := false
		for g := range goldSet {
			if strings.HasSuffix(norm, g) || strings.HasSuffix(g, norm) || strings.Contains(norm, g) {
				matched = true
				coveredGold[g] = true
				break
			}
		}

		if matched {
			if firstRank == 0 {
				firstRank = i + 1
			}
			if i < 1 {
				hitsR1++
			}
			if i < 5 {
				hitsR5++
			}
			if i < 10 {
				hitsR10++
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

	r1 := math.Min(1.0, float64(hitsR1)/float64(totalGold))
	r5 := math.Min(1.0, float64(hitsR5)/float64(totalGold))
	r10 := math.Min(1.0, float64(hitsR10)/float64(totalGold))
	cov := float64(len(coveredGold)) / float64(totalGold)

	return StrategyResult{
		Recall1:  r1,
		Recall5:  r5,
		Recall10: r10,
		MRR:      mrr,
		Coverage: cov,
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
func RenderMarkdownReport(summaries map[string]SummaryResult, taskCount int) string {
	var sb strings.Builder
	sb.WriteString("# ContextBench / SWE-Bench Retrieval Benchmark Report\n\n")
	sb.WriteString(fmt.Sprintf("> Evaluated across **%d tasks** comparing BM25 keyword search, DiskANN+PQ vector search, and GraphRAG.\n\n", taskCount))

	sb.WriteString("| Strategy | Recall@1 | Recall@5 | Recall@10 | MRR | Coverage | Avg Tokens | Token Savings | Latency |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |\n")

	// Order strategies predictably: BM25 first, then Vector, Hybrid, GraphRAG
	ordered := []string{"BM25", "Vector (DiskANN+PQ)", "Vector (HNSW)", "Hybrid", "GraphRAG"}
	seen := make(map[string]bool)

	for _, name := range ordered {
		if s, ok := summaries[name]; ok {
			seen[name] = true
			savings := fmt.Sprintf("%.1f%%", s.TokenSavings)
			if s.TokenSavings > 0 {
				savings = "**-" + savings + "**"
			}
			sb.WriteString(fmt.Sprintf("| **%s** | %.1f%% | %.1f%% | %.1f%% | %.3f | %.1f%% | %d | %s | %.1fms |\n",
				s.Strategy, s.AvgRecall1, s.AvgRecall5, s.AvgRecall10, s.AvgMRR, s.AvgCoverage, s.AvgTokens, savings, s.AvgLatencyMs,
			))
		}
	}

	// Add any other strategies not in the default order
	for name, s := range summaries {
		if !seen[name] {
			sb.WriteString(fmt.Sprintf("| **%s** | %.1f%% | %.1f%% | %.1f%% | %.3f | %.1f%% | %d | %.1f%% | %.1fms |\n",
				s.Strategy, s.AvgRecall1, s.AvgRecall5, s.AvgRecall10, s.AvgMRR, s.AvgCoverage, s.AvgTokens, s.TokenSavings, s.AvgLatencyMs,
			))
		}
	}

	sb.WriteString("\n### Key Takeaways\n\n")
	if gr, ok := summaries["GraphRAG"]; ok {
		if bm, ok := summaries["BM25"]; ok && bm.AvgRecall5 > 0 {
			gain := gr.AvgRecall5 / bm.AvgRecall5
			sb.WriteString(fmt.Sprintf("- 🚀 **%.1fx Higher Recall**: GraphRAG achieved %.1f%% Recall@5 vs BM25's %.1f%%.\n", gain, gr.AvgRecall5, bm.AvgRecall5))
		}
		if gr.TokenSavings > 0 {
			sb.WriteString(fmt.Sprintf("- 💰 **%.1f%% Token Savings**: Agents navigate directly to relevant definitions, saving context tokens.\n", gr.TokenSavings))
		}
	} else if vec, ok := summaries["Vector (DiskANN+PQ)"]; ok {
		if bm, ok := summaries["BM25"]; ok && bm.AvgRecall5 > 0 {
			gain := vec.AvgRecall5 / bm.AvgRecall5
			sb.WriteString(fmt.Sprintf("- 🚀 **%.1fx Higher Recall**: DiskANN+PQ achieved %.1f%% Recall@5 vs BM25's %.1f%%.\n", gain, vec.AvgRecall5, bm.AvgRecall5))
		}
	}

	return sb.String()
}

// RenderJSON returns the summaries serialized as indented JSON.
func RenderJSON(summaries map[string]SummaryResult) (string, error) {
	b, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
