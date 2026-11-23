package benchmark_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/benchmark"
)

func TestComparePairedMetricsSynthetic(t *testing.T) {
	// 5 tasks with known synthetic differences:
	// Task 1: Base MRR 0.2, Cand MRR 0.4 (+0.2, Win)
	// Task 2: Base MRR 0.4, Cand MRR 0.6 (+0.2, Win)
	// Task 3: Base MRR 0.6, Cand MRR 0.8 (+0.2, Win)
	// Task 4: Base MRR 0.8, Cand MRR 1.0 (+0.2, Win)
	// Task 5: Base MRR 1.0, Cand MRR 1.0 (+0.0, Tie)
	base := []benchmark.PerTaskRecord{
		{Task: "t1", Strategy: "Base", MRR: 0.2, R1: 0, R5: 1, R10: 1, Tokens: 100},
		{Task: "t2", Strategy: "Base", MRR: 0.4, R1: 0, R5: 1, R10: 1, Tokens: 100},
		{Task: "t3", Strategy: "Base", MRR: 0.6, R1: 0, R5: 0, R10: 1, Tokens: 100},
		{Task: "t4", Strategy: "Base", MRR: 0.8, R1: 0, R5: 0, R10: 1, Tokens: 100},
		{Task: "t5", Strategy: "Base", MRR: 1.0, R1: 1, R5: 1, R10: 1, Tokens: 100},
	}
	cand := []benchmark.PerTaskRecord{
		{Task: "t1", Strategy: "Cand", MRR: 0.4, R1: 0, R5: 1, R10: 1, Tokens: 80},
		{Task: "t2", Strategy: "Cand", MRR: 0.6, R1: 0, R5: 1, R10: 1, Tokens: 80},
		{Task: "t3", Strategy: "Cand", MRR: 0.8, R1: 0, R5: 1, R10: 1, Tokens: 80},
		{Task: "t4", Strategy: "Cand", MRR: 1.0, R1: 1, R5: 1, R10: 1, Tokens: 80},
		{Task: "t5", Strategy: "Cand", MRR: 1.0, R1: 1, R5: 1, R10: 1, Tokens: 80},
	}

	res, err := benchmark.CompareRecords(base, cand, "Base", "Cand")
	if err != nil {
		t.Fatalf("CompareRecords failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil ComparisonResult")
	}

	if res.BaselineName != "Base" || res.CandidateName != "Cand" || res.TaskCount != 5 {
		t.Errorf("metadata mismatch: %+v", res)
	}

	var mrrStats *benchmark.PairedMetricStats
	for i := range res.Metrics {
		if res.Metrics[i].Metric == "MRR" {
			mrrStats = &res.Metrics[i]
			break
		}
	}
	if mrrStats == nil {
		t.Fatal("missing MRR in comparison metrics")
	}

	// Mean baseline = (0.2+0.4+0.6+0.8+1.0)/5 = 0.600
	// Mean candidate = (0.4+0.6+0.8+1.0+1.0)/5 = 0.760
	// Paired diff = +0.160
	if math.Abs(mrrStats.Baseline-0.600) > 1e-4 {
		t.Errorf("Baseline MRR = %f, want 0.600", mrrStats.Baseline)
	}
	if math.Abs(mrrStats.Candidate-0.760) > 1e-4 {
		t.Errorf("Candidate MRR = %f, want 0.760", mrrStats.Candidate)
	}
	if math.Abs(mrrStats.Diff-0.160) > 1e-4 {
		t.Errorf("Diff MRR = %f, want 0.160", mrrStats.Diff)
	}

	// Sign test: Wins = 4, Losses = 0, Ties = 1.
	// 2 * (0.5)^4 = 2 * 0.0625 = 0.125
	if mrrStats.Wins != 4 || mrrStats.Losses != 0 || mrrStats.Ties != 1 {
		t.Errorf("Wins/Losses/Ties = %d/%d/%d, want 4/0/1", mrrStats.Wins, mrrStats.Losses, mrrStats.Ties)
	}
	if math.Abs(mrrStats.PValue-0.125) > 1e-4 {
		t.Errorf("p-value = %f, want 0.125", mrrStats.PValue)
	}

	// Bootstrap 95% CI should contain +0.160 and be strictly positive
	if mrrStats.CILower <= 0 || mrrStats.CIUpper <= mrrStats.CILower {
		t.Errorf("unexpected CI: [%f, %f]", mrrStats.CILower, mrrStats.CIUpper)
	}

	md := res.FormatMarkdown()
	if !strings.Contains(md, "Paired comparison: Cand vs Base") || !strings.Contains(md, "Sign Test") {
		t.Errorf("FormatMarkdown lacks required sections:\n%s", md)
	}
}

func TestReadPerTaskRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pertask.jsonl")
	content := `{"task":"t1","strategy":"BM25","r1":0.5,"r5":1.0,"r10":1.0,"mrr":0.5,"tokens":120,"latency_ms":10.5}
{"task":"t2","strategy":"BM25","r1":1.0,"r5":1.0,"r10":1.0,"mrr":1.0,"tokens":150,"latency_ms":12.0}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := benchmark.ReadPerTaskRecords(path)
	if err != nil {
		t.Fatalf("ReadPerTaskRecords error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if recs[0].Task != "t1" || recs[0].Strategy != "BM25" || recs[0].MRR != 0.5 {
		t.Errorf("record 0 mismatch: %+v", recs[0])
	}
}

func TestReadPerTaskRecordsValidation(t *testing.T) {
	dir := t.TempDir()

	writeJSONL := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// 1. Missing task ID
	p1 := writeJSONL("missing_task.jsonl", "{\"strategy\":\"BM25\",\"mrr\":0.5}\n")
	if _, err := benchmark.ReadPerTaskRecords(p1); err == nil {
		t.Errorf("expected error for record without task field, got nil")
	} else if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "task") {
		t.Errorf("expected line 1 error mentioning task, got: %v", err)
	}

	// 2. Empty task ID
	p2 := writeJSONL("empty_task.jsonl", "{\"task\":\"  \",\"strategy\":\"BM25\",\"mrr\":0.5}\n")
	if _, err := benchmark.ReadPerTaskRecords(p2); err == nil {
		t.Errorf("expected error for record with empty task field, got nil")
	} else if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "task") {
		t.Errorf("expected line 1 error mentioning task, got: %v", err)
	}

	// 3. Missing strategy
	p3 := writeJSONL("missing_strategy.jsonl", "{\"task\":\"t1\",\"mrr\":0.5}\n")
	if _, err := benchmark.ReadPerTaskRecords(p3); err == nil {
		t.Errorf("expected error for record without strategy, got nil")
	} else if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "strategy") {
		t.Errorf("expected line 1 error mentioning strategy, got: %v", err)
	}

	// 4. Record without metrics
	p4 := writeJSONL("no_metrics.jsonl", "{\"task\":\"t1\",\"strategy\":\"BM25\"}\n")
	if _, err := benchmark.ReadPerTaskRecords(p4); err == nil {
		t.Errorf("expected error for record without metrics, got nil")
	} else if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "metric") {
		t.Errorf("expected line 1 error mentioning metrics, got: %v", err)
	}

	// 5. Valid record with explicit zero metrics
	p5 := writeJSONL("zero_metrics.jsonl", "{\"task\":\"t1\",\"strategy\":\"BM25\",\"r1\":0,\"mrr\":0}\n")
	recs, err := benchmark.ReadPerTaskRecords(p5)
	if err != nil {
		t.Fatalf("unexpected error for explicit zero metrics: %v", err)
	}
	if len(recs) != 1 || recs[0].R1 != 0 || recs[0].MRR != 0 {
		t.Errorf("unexpected record for zero metrics: %+v", recs)
	}
}

func TestComparePublished121TasksReproducer(t *testing.T) {
	// Reproduce the published pair on 121 tasks from docs/benchmarks.md:
	// Hybrid: R10 72.2%, MRR 0.502
	// Hybrid (1 chunk/file): R10 75.9%, MRR 0.538
	// Recall@10 diff = +3.7% (+0.037), MRR diff = +0.036
	n := 121
	base := make([]benchmark.PerTaskRecord, n)
	cand := make([]benchmark.PerTaskRecord, n)

	// In 121 tasks, a difference of 4 tasks is 4/121 ~ 3.3%, 5 tasks is 5/121 ~ 4.13%.
	// 87 tasks base R10=1, 92 tasks cand R10=1 -> base=71.9%, cand=76.0%, diff=+4.1%.
	// Let's create exact matching synthetic rows:
	for i := 0; i < n; i++ {
		taskID := "task-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		var r10Base, r10Cand float64
		var mrrBase, mrrCand float64

		// 87 tasks where base has R10=1
		if i < 87 {
			r10Base = 1.0
		}
		// 92 tasks where cand has R10=1 (includes the 87 plus 5 more wins)
		if i < 92 {
			r10Cand = 1.0
		}

		// Base MRR ~ 0.502
		mrrBase = 0.502
		// Cand MRR ~ 0.538
		mrrCand = 0.538
		if i < 10 {
			mrrCand += 0.2 // cand wins
		} else if i < 15 {
			mrrCand -= 0.1 // cand losses
		}

		base[i] = benchmark.PerTaskRecord{Task: taskID, Strategy: "Hybrid", R10: r10Base, MRR: mrrBase}
		cand[i] = benchmark.PerTaskRecord{Task: taskID, Strategy: "Hybrid (1 chunk/file)", R10: r10Cand, MRR: mrrCand}
	}

	res, err := benchmark.CompareRecords(base, cand, "Hybrid", "Hybrid (1 chunk/file)")
	if err != nil {
		t.Fatalf("CompareRecords error: %v", err)
	}

	var r10Stats *benchmark.PairedMetricStats
	for i := range res.Metrics {
		if res.Metrics[i].Metric == "Recall@10" {
			r10Stats = &res.Metrics[i]
			break
		}
	}
	if r10Stats == nil {
		t.Fatal("missing Recall@10 stats")
	}

	// Diff should be approximately +4.1%
	if r10Stats.Diff <= 0 {
		t.Errorf("expected positive Recall@10 diff, got %f", r10Stats.Diff)
	}
	// Sign test: cand has 5 wins, 0 losses, 116 ties.
	if r10Stats.Wins != 5 || r10Stats.Losses != 0 || r10Stats.Ties != 116 {
		t.Errorf("Wins/Losses/Ties = %d/%d/%d, want 5/0/116", r10Stats.Wins, r10Stats.Losses, r10Stats.Ties)
	}
	// 2 * (0.5)^5 = 0.0625 (p ~ 0.0625)
	if math.Abs(r10Stats.PValue-0.0625) > 1e-4 {
		t.Errorf("p-value = %f, want 0.0625", r10Stats.PValue)
	}
	// 95% Bootstrap CI should span the interval
	if r10Stats.CILower < 0 || r10Stats.CIUpper <= r10Stats.CILower {
		t.Errorf("unexpected CI: [%f, %f]", r10Stats.CILower, r10Stats.CIUpper)
	}
}

func TestCompareRecordsRejectsDuplicateTaskIDs(t *testing.T) {
	dupBase := []benchmark.PerTaskRecord{
		{Task: "t1", Strategy: "Base", MRR: 0.2, R1: 0},
		{Task: "t1", Strategy: "Base", MRR: 0.8, R1: 1},
	}
	cand := []benchmark.PerTaskRecord{
		{Task: "t1", Strategy: "Cand", MRR: 0.5, R1: 1},
	}

	_, err := benchmark.CompareRecords(dupBase, cand, "Base", "Cand")
	if err == nil {
		t.Fatalf("expected error on duplicate task ID in baseline records, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate task \"t1\"") {
		t.Errorf("expected error mentioning duplicate task ID \"t1\", got: %v", err)
	}

	validBase := []benchmark.PerTaskRecord{
		{Task: "t1", Strategy: "Base", MRR: 0.2, R1: 0},
	}
	dupCand := []benchmark.PerTaskRecord{
		{Task: "t1", Strategy: "Cand", MRR: 0.5, R1: 1},
		{Task: "t1", Strategy: "Cand", MRR: 0.9, R1: 1},
	}

	_, err = benchmark.CompareRecords(validBase, dupCand, "Base", "Cand")
	if err == nil {
		t.Fatalf("expected error on duplicate task ID in candidate records, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate task \"t1\"") {
		t.Errorf("expected error mentioning duplicate task ID \"t1\", got: %v", err)
	}
}

func TestCompareRecordsPartialMetricsNotZeroFilled(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "full.jsonl")
	partialPath := filepath.Join(dir, "partial.jsonl")

	fullContent := `{"task":"t1","strategy":"A","mrr":0.8,"tokens":100}
`
	partialContent := `{"task":"t1","strategy":"B","tokens":100}
`
	if err := os.WriteFile(fullPath, []byte(fullContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPath, []byte(partialContent), 0o644); err != nil {
		t.Fatal(err)
	}

	fullRecs, err := benchmark.ReadPerTaskRecords(fullPath)
	if err != nil {
		t.Fatalf("ReadPerTaskRecords(full) failed: %v", err)
	}
	partialRecs, err := benchmark.ReadPerTaskRecords(partialPath)
	if err != nil {
		t.Fatalf("ReadPerTaskRecords(partial) failed: %v", err)
	}

	res, err := benchmark.CompareRecords(fullRecs, partialRecs, "A", "B")
	if err != nil {
		t.Fatalf("CompareRecords failed: %v", err)
	}

	for _, m := range res.Metrics {
		if m.Metric == "MRR" {
			t.Fatalf("DEFECT: missing MRR in strategy B was zero-filled and reported as measured (Candidate=%f, Diff=%f)", m.Candidate, m.Diff)
		}
	}

	// Verify explicit 0.0 metric is preserved and compared
	zeroPath := filepath.Join(dir, "zero.jsonl")
	zeroContent := `{"task":"t1","strategy":"B","mrr":0.0,"tokens":100}
`
	if err := os.WriteFile(zeroPath, []byte(zeroContent), 0o644); err != nil {
		t.Fatal(err)
	}
	zeroRecs, err := benchmark.ReadPerTaskRecords(zeroPath)
	if err != nil {
		t.Fatalf("ReadPerTaskRecords(zero) failed: %v", err)
	}
	resZero, err := benchmark.CompareRecords(fullRecs, zeroRecs, "A", "B")
	if err != nil {
		t.Fatalf("CompareRecords(full, zero) failed: %v", err)
	}
	var mrrZero *benchmark.PairedMetricStats
	for i := range resZero.Metrics {
		if resZero.Metrics[i].Metric == "MRR" {
			mrrZero = &resZero.Metrics[i]
			break
		}
	}
	if mrrZero == nil {
		t.Fatalf("expected explicit 0.0 MRR to be compared, but MRR was missing from metrics")
	}
	if mrrZero.Candidate != 0.0 || mrrZero.Diff != -0.8 {
		t.Errorf("expected explicit 0.0 Candidate MRR and Diff -0.8, got Cand=%f Diff=%f", mrrZero.Candidate, mrrZero.Diff)
	}
}
