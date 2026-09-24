package benchmark

import (
	"context"
	"testing"
)

func TestRunAgentEvaluation(t *testing.T) {
	ctx := context.Background()
	summary, err := RunAgentEvaluation(ctx, nil)
	if err != nil {
		t.Fatalf("RunAgentEvaluation failed: %v", err)
	}

	if summary.TotalTasks != 4 {
		t.Errorf("expected 4 tasks, got %d", summary.TotalTasks)
	}

	if summary.GleannSuccessRate != 100.0 {
		t.Errorf("expected 100%% gleann success rate, got %.1f%%", summary.GleannSuccessRate)
	}

	if summary.BaselineSuccessRate >= summary.GleannSuccessRate {
		t.Errorf("expected baseline success rate < gleann success rate, got %.1f%% vs %.1f%%",
			summary.BaselineSuccessRate, summary.GleannSuccessRate)
	}

	if summary.AvgTokenSavingsPct < 80.0 {
		t.Errorf("expected avg token savings > 80%%, got %.1f%%", summary.AvgTokenSavingsPct)
	}

	if summary.AvgToolCallReductionPct < 70.0 {
		t.Errorf("expected avg tool call reduction > 70%%, got %.1f%%", summary.AvgToolCallReductionPct)
	}

	md := summary.FormatMarkdown()
	if len(md) == 0 {
		t.Error("FormatMarkdown returned empty string")
	}

	// Verify each task has positive token savings
	for _, task := range summary.Tasks {
		if task.TokenReductionPct <= 0 {
			t.Errorf("task %s expected positive token reduction, got %.1f%%", task.TaskID, task.TokenReductionPct)
		}
		if task.ToolCallReductionPct <= 0 {
			t.Errorf("task %s expected positive tool call reduction, got %.1f%%", task.TaskID, task.ToolCallReductionPct)
		}
	}
}
