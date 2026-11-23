package benchmark

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
)

// ErrStrategyUnavailable indicates a retrieval strategy cannot run because
// an external prerequisite (e.g. the ripgrep binary) is missing.
var ErrStrategyUnavailable = errors.New("strategy unavailable")

// PerTaskRecord holds task-level metrics for a strategy from a benchmark run.
type PerTaskRecord struct {
	Task         string  `json:"task"`
	Strategy     string  `json:"strategy"`
	R1           float64 `json:"r1"`
	R5           float64 `json:"r5"`
	R10          float64 `json:"r10"`
	MRR          float64 `json:"mrr"`
	Tokens       int     `json:"tokens"`
	LatencyMs    float64 `json:"latency_ms"`
	HasR1        bool    `json:"-"`
	HasR5        bool    `json:"-"`
	HasR10       bool    `json:"-"`
	HasMRR       bool    `json:"-"`
	HasTokens    bool    `json:"-"`
	HasLatencyMs bool    `json:"-"`
}

// HasExplicitMetrics reports whether any explicit metric presence flag is set.
func (r PerTaskRecord) HasExplicitMetrics() bool {
	return r.HasR1 || r.HasR5 || r.HasR10 || r.HasMRR || r.HasTokens || r.HasLatencyMs
}

func (r PerTaskRecord) hasMetric(name string) bool {
	if !r.HasExplicitMetrics() {
		return true
	}
	switch name {
	case "MRR":
		return r.HasMRR
	case "Recall@1":
		return r.HasR1
	case "Recall@5":
		return r.HasR5
	case "Recall@10":
		return r.HasR10
	case "Avg Tokens":
		return r.HasTokens
	case "LatencyMs":
		return r.HasLatencyMs
	default:
		return false
	}
}

// PairedMetricStats contains paired statistical comparisons for a single metric.
type PairedMetricStats struct {
	Metric      string  `json:"metric"`
	Baseline    float64 `json:"baseline"`
	Candidate   float64 `json:"candidate"`
	Diff        float64 `json:"diff"`
	CILower     float64 `json:"ci_lower"`
	CIUpper     float64 `json:"ci_upper"`
	PValue      float64 `json:"p_value"`
	Significant bool    `json:"significant"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Ties        int     `json:"ties"`
}

// ComparisonResult holds paired comparisons between a baseline and candidate strategy.
type ComparisonResult struct {
	BaselineName  string              `json:"baseline_name"`
	CandidateName string              `json:"candidate_name"`
	TaskCount     int                 `json:"task_count"`
	Metrics       []PairedMetricStats `json:"metrics"`
}

// ReadPerTaskRecords reads per-task JSONL records from a file.
func ReadPerTaskRecords(path string) ([]PerTaskRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening per-task file: %w", err)
	}
	defer f.Close()

	type rawPerTaskRecord struct {
		Task      *string  `json:"task"`
		Strategy  *string  `json:"strategy"`
		R1        *float64 `json:"r1"`
		R5        *float64 `json:"r5"`
		R10       *float64 `json:"r10"`
		MRR       *float64 `json:"mrr"`
		Tokens    *int     `json:"tokens"`
		LatencyMs *float64 `json:"latency_ms"`
	}

	var records []PerTaskRecord
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var raw rawPerTaskRecord
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("parsing per-task file line %d: %w", lineNum, err)
		}
		if raw.Task == nil || strings.TrimSpace(*raw.Task) == "" {
			return nil, fmt.Errorf("parsing per-task file line %d: empty or missing task ID", lineNum)
		}
		if raw.Strategy == nil || strings.TrimSpace(*raw.Strategy) == "" {
			return nil, fmt.Errorf("parsing per-task file line %d: empty or missing strategy", lineNum)
		}
		if raw.R1 == nil && raw.R5 == nil && raw.R10 == nil && raw.MRR == nil && raw.Tokens == nil && raw.LatencyMs == nil {
			return nil, fmt.Errorf("parsing per-task file line %d: record contains no metric fields", lineNum)
		}
		rec := PerTaskRecord{
			Task:     strings.TrimSpace(*raw.Task),
			Strategy: strings.TrimSpace(*raw.Strategy),
		}
		if raw.R1 != nil {
			rec.R1 = *raw.R1
			rec.HasR1 = true
		}
		if raw.R5 != nil {
			rec.R5 = *raw.R5
			rec.HasR5 = true
		}
		if raw.R10 != nil {
			rec.R10 = *raw.R10
			rec.HasR10 = true
		}
		if raw.MRR != nil {
			rec.MRR = *raw.MRR
			rec.HasMRR = true
		}
		if raw.Tokens != nil {
			rec.Tokens = *raw.Tokens
			rec.HasTokens = true
		}
		if raw.LatencyMs != nil {
			rec.LatencyMs = *raw.LatencyMs
			rec.HasLatencyMs = true
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading per-task file: %w", err)
	}
	return records, nil
}

// CompareRecords computes paired statistics between two sets of per-task records.
func CompareRecords(baseRecords, candRecords []PerTaskRecord, baseName, candName string) (*ComparisonResult, error) {
	baseByTask := make(map[string]PerTaskRecord, len(baseRecords))
	for _, r := range baseRecords {
		if _, exists := baseByTask[r.Task]; exists {
			return nil, fmt.Errorf("duplicate task %q in baseline records", r.Task)
		}
		baseByTask[r.Task] = r
	}
	candByTask := make(map[string]PerTaskRecord, len(candRecords))
	for _, r := range candRecords {
		if _, exists := candByTask[r.Task]; exists {
			return nil, fmt.Errorf("duplicate task %q in candidate records", r.Task)
		}
		candByTask[r.Task] = r
	}

	var commonTasks []string
	for t := range baseByTask {
		if _, ok := candByTask[t]; ok {
			commonTasks = append(commonTasks, t)
		}
	}
	sort.Strings(commonTasks)
	if len(commonTasks) == 0 {
		return nil, fmt.Errorf("no matching tasks between %q and %q", baseName, candName)
	}
	n := len(commonTasks)

	type metricDef struct {
		name      string
		isPercent bool
		decimals  int
		get       func(PerTaskRecord) float64
	}
	defs := []metricDef{
		{"MRR", false, 3, func(r PerTaskRecord) float64 { return r.MRR }},
		{"Recall@1", true, 1, func(r PerTaskRecord) float64 { return r.R1 }},
		{"Recall@5", true, 1, func(r PerTaskRecord) float64 { return r.R5 }},
		{"Recall@10", true, 1, func(r PerTaskRecord) float64 { return r.R10 }},
		{"Avg Tokens", false, 0, func(r PerTaskRecord) float64 { return float64(r.Tokens) }},
	}

	result := &ComparisonResult{
		BaselineName:  baseName,
		CandidateName: candName,
		TaskCount:     n,
		Metrics:       make([]PairedMetricStats, 0, len(defs)),
	}

	const eps = 1e-9
	for _, def := range defs {
		hasMetric := true
		for _, t := range commonTasks {
			if !baseByTask[t].hasMetric(def.name) || !candByTask[t].hasMetric(def.name) {
				hasMetric = false
				break
			}
		}
		if !hasMetric {
			continue
		}

		baseVals := make([]float64, n)
		candVals := make([]float64, n)
		diffs := make([]float64, n)
		sumBase, sumCand, sumDiff := 0.0, 0.0, 0.0
		wins, losses, ties := 0, 0, 0

		for i, task := range commonTasks {
			b := def.get(baseByTask[task])
			c := def.get(candByTask[task])
			d := c - b
			baseVals[i] = b
			candVals[i] = c
			diffs[i] = d
			sumBase += b
			sumCand += c
			sumDiff += d

			if d > eps {
				wins++
			} else if d < -eps {
				losses++
			} else {
				ties++
			}
		}

		meanBase := sumBase / float64(n)
		meanCand := sumCand / float64(n)
		meanDiff := sumDiff / float64(n)

		// Deterministic bootstrap CI (B = 2000, fixed seed 42)
		const B = 2000
		rng := rand.New(rand.NewSource(42))
		bootMeans := make([]float64, B)
		for b := 0; b < B; b++ {
			s := 0.0
			for j := 0; j < n; j++ {
				s += diffs[rng.Intn(n)]
			}
			bootMeans[b] = s / float64(n)
		}
		sort.Float64s(bootMeans)
		ciLower := bootMeans[int(math.Floor(0.025*B))]
		ciUpper := bootMeans[int(math.Floor(0.975*B))]

		// Exact two-sided sign test
		nNonTied := wins + losses
		k := min(wins, losses)
		pVal := computeSignTestPValue(nNonTied, k)

		if def.isPercent {
			meanBase *= 100
			meanCand *= 100
			meanDiff *= 100
			ciLower *= 100
			ciUpper *= 100
		}

		result.Metrics = append(result.Metrics, PairedMetricStats{
			Metric:      def.name,
			Baseline:    roundDigits(meanBase, def.decimals),
			Candidate:   roundDigits(meanCand, def.decimals),
			Diff:        roundDigits(meanDiff, def.decimals),
			CILower:     roundDigits(ciLower, def.decimals),
			CIUpper:     roundDigits(ciUpper, def.decimals),
			PValue:      math.Round(pVal*10000) / 10000,
			Significant: pVal < 0.05,
			Wins:        wins,
			Losses:      losses,
			Ties:        ties,
		})
	}

	if len(result.Metrics) == 0 {
		return nil, fmt.Errorf("no common metrics found between %q and %q", baseName, candName)
	}

	return result, nil
}

// computeSignTestPValue calculates the two-sided p-value for a sign test
// with n non-tied pairs and k successes, using exact binomial probabilities.
func computeSignTestPValue(n, k int) float64 {
	if n == 0 {
		return 1.0
	}
	total := 0.0
	ln2 := math.Ln2
	for j := 0; j <= k; j++ {
		logComb := logBinomial(n, j)
		logProb := logComb - float64(n)*ln2
		total += math.Exp(logProb)
	}
	p := 2.0 * total
	if p > 1.0 {
		p = 1.0
	}
	return p
}

func logBinomial(n, k int) float64 {
	if k < 0 || k > n {
		return math.Inf(-1)
	}
	lgN, _ := math.Lgamma(float64(n + 1))
	lgK, _ := math.Lgamma(float64(k + 1))
	lgNK, _ := math.Lgamma(float64(n - k + 1))
	return lgN - lgK - lgNK
}

func roundDigits(val float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(val*pow) / pow
}

// FormatMarkdown formats the comparison result into GitHub-flavored Markdown.
func (c *ComparisonResult) FormatMarkdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Paired comparison: %s vs %s (%d tasks)\n\n",
		c.CandidateName, c.BaselineName, c.TaskCount))
	sb.WriteString("| Metric | Baseline | Candidate | Diff | 95% Bootstrap CI | Sign Test (W/L/T) | p-value |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: | :---: | :---: | :---: |\n")

	for _, m := range c.Metrics {
		isPct := strings.HasPrefix(m.Metric, "Recall")
		pct := ""
		if isPct {
			pct = "%"
		}

		diffSign := ""
		if m.Diff > 0 {
			diffSign = "+"
		}
		ciLowSign := ""
		if m.CILower > 0 {
			ciLowSign = "+"
		}
		ciHighSign := ""
		if m.CIUpper > 0 {
			ciHighSign = "+"
		}

		pStr := fmt.Sprintf("p=%.4f", m.PValue)
		if m.PValue < 0.001 {
			pStr = "p<0.001"
		}
		if m.Significant {
			pStr += " *"
		}

		decimals := 3
		if isPct {
			decimals = 1
		} else if m.Metric == "Avg Tokens" {
			decimals = 0
		}

		baseStr := fmt.Sprintf("%.*f%s", decimals, m.Baseline, pct)
		candStr := fmt.Sprintf("%.*f%s", decimals, m.Candidate, pct)
		diffStr := fmt.Sprintf("%s%.*f%s", diffSign, decimals, m.Diff, pct)
		ciStr := fmt.Sprintf("[%s%.*f%s, %s%.*f%s]", ciLowSign, decimals, m.CILower, pct, ciHighSign, decimals, m.CIUpper, pct)
		signStr := fmt.Sprintf("%d / %d / %d", m.Wins, m.Losses, m.Ties)

		sb.WriteString(fmt.Sprintf("| **%s** | %s | %s | %s | %s | %s | %s |\n",
			m.Metric, baseStr, candStr, diffStr, ciStr, signStr, pStr))
	}
	sb.WriteString("\n")
	return sb.String()
}
