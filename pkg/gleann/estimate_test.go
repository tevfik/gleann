package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGrahamListScheduling(t *testing.T) {
	// Total bytes: 100 MB, largest file: 20 MB, rate: 10 MB/s, workers: 4
	totalBytes := int64(100 * 1024 * 1024)
	largestFile := int64(20 * 1024 * 1024)
	rate := float64(10 * 1024 * 1024) // 10 MB/s
	workers := 4

	low, high := ComputeMakespanBounds(totalBytes, largestFile, rate, workers)

	// totalTime = 10s. maxTime = 2s.
	// low = max(10/4, 2) = max(2.5, 2) = 2.5s
	// high = 10/4 + (1 - 1/4)*2 = 2.5 + 0.75*2 = 2.5 + 1.5 = 4.0s
	expectedLow := 2.5
	expectedHigh := 4.0

	if low < expectedLow-0.01 || low > expectedLow+0.01 {
		t.Errorf("expected low=%.2f, got %.2f", expectedLow, low)
	}
	if high < expectedHigh-0.01 || high > expectedHigh+0.01 {
		t.Errorf("expected high=%.2f, got %.2f", expectedHigh, high)
	}
	if low > high {
		t.Errorf("invariant violated: low (%.2f) > high (%.2f)", low, high)
	}
}

func TestEstimateJob_WalkAndCompute(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 10 files with known sizes
	for i := 0; i < 10; i++ {
		p := filepath.Join(tmpDir, filepath.Base(tmpDir)+string(rune('a'+i))+".txt")
		content := make([]byte, (i+1)*1024) // 1KB, 2KB, ..., 10KB
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	est, err := EstimateJob(tmpDir, 2)
	if err != nil {
		t.Fatalf("EstimateJob failed: %v", err)
	}

	if est.FileCount != 10 {
		t.Errorf("expected 10 files, got %d", est.FileCount)
	}

	expectedBytes := int64(55 * 1024) // sum(1..10) KB = 55 KB
	if est.TotalBytes != expectedBytes {
		t.Errorf("expected %d bytes, got %d", expectedBytes, est.TotalBytes)
	}

	if est.LargestFileBytes != int64(10*1024) {
		t.Errorf("expected largest file 10KB, got %d", est.LargestFileBytes)
	}

	if est.EstimatedSecLow <= 0 || est.EstimatedSecHigh <= 0 {
		t.Errorf("expected positive estimated bounds, got low=%.4f, high=%.4f",
			est.EstimatedSecLow, est.EstimatedSecHigh)
	}

	if est.EstimatedSecLow > est.EstimatedSecHigh {
		t.Errorf("expected low <= high, got low=%.4f, high=%.4f",
			est.EstimatedSecLow, est.EstimatedSecHigh)
	}
}

func TestEstimateDecisionGate(t *testing.T) {
	est := &JobEstimate{
		FileCount:        5000,
		TotalBytes:       200 * 1024 * 1024,
		EstimatedSecLow:  120.0,
		EstimatedSecHigh: 240.0, // 4 minutes
	}

	// Case 1: Threshold 5 minutes (300s) -> No gate trigger
	gate1 := EvaluateDecisionGate(est, 5.0, "")
	if gate1.Triggered {
		t.Error("expected gate not to trigger for 5m threshold on 4m job")
	}

	// Case 2: Threshold 2 minutes (120s) -> Gate triggered with exit code 4
	gate2 := EvaluateDecisionGate(est, 2.0, "")
	if !gate2.Triggered {
		t.Error("expected gate to trigger for 2m threshold on 4m job")
	}
	if gate2.ExitCode != 4 {
		t.Errorf("expected exit code 4, got %d", gate2.ExitCode)
	}
	if len(gate2.Options) != 3 {
		t.Errorf("expected 3 options [proceed, fast, cancel], got: %v", gate2.Options)
	}

	// Case 3: Approved with proceed
	gate3 := EvaluateDecisionGate(est, 2.0, "proceed")
	if gate3.Triggered {
		t.Error("approved gate should not be triggered")
	}
	if gate3.Action != GateActionProceed {
		t.Errorf("expected action proceed, got %v", gate3.Action)
	}

	// Case 4: Approved with fast
	gate4 := EvaluateDecisionGate(est, 2.0, "fast")
	if gate4.Action != GateActionFast {
		t.Errorf("expected action fast, got %v", gate4.Action)
	}

	// Case 5: Approved with cancel
	gate5 := EvaluateDecisionGate(est, 2.0, "cancel")
	if gate5.Action != GateActionCancel {
		t.Errorf("expected action cancel, got %v", gate5.Action)
	}
}
