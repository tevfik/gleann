package benchmark

import (
	"testing"
	"time"
)

func TestEvaluateTask_DuplicateChunksDoNotInflateRecall(t *testing.T) {
	task := Task{GoldFiles: []string{"a/one.go", "b/two.go"}}
	// Three chunks from one gold file must not count as both gold files.
	got := evaluateTask(task, []string{"a/one.go", "a/one.go", "./a/one.go"}, 0, time.Millisecond)
	if got.Recall5 != 0.5 || got.Coverage != 0.5 {
		t.Fatalf("recall5=%v coverage=%v, want 0.5/0.5", got.Recall5, got.Coverage)
	}
}

func TestEvaluateTask_RanksAreFileLevel(t *testing.T) {
	task := Task{GoldFiles: []string{"b/two.go"}}
	// The two chunks of x.go occupy one rank, so two.go is rank 2.
	got := evaluateTask(task, []string{"x.go", "x.go", "b/two.go"}, 0, 0)
	if got.MRR != 0.5 || got.Recall1 != 0 || got.Recall5 != 1 {
		t.Fatalf("mrr=%v r1=%v r5=%v, want 0.5/0/1", got.MRR, got.Recall1, got.Recall5)
	}
}

func TestEvaluateTask_NoHits(t *testing.T) {
	got := evaluateTask(Task{GoldFiles: []string{"a.go"}}, []string{"b.go"}, 0, 0)
	if got.MRR != 0 || got.Coverage != 0 {
		t.Fatalf("unexpected %+v", got)
	}
}
