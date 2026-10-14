package gleann

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestComputeMakespanBounds_Table(t *testing.T) {
	const mb = 1024 * 1024
	tests := []struct {
		name           string
		total, largest int64
		rate           float64
		workers        int
		wantLo, wantHi float64
	}{
		{"single worker is exact", 100 * mb, 20 * mb, 10 * mb, 1, 10, 10},
		{"zero workers treated as one", 100 * mb, 20 * mb, 10 * mb, 0, 10, 10},
		{"negative workers treated as one", 100 * mb, 20 * mb, 10 * mb, -3, 10, 10},
		{"largest dominates", 30 * mb, 20 * mb, 10 * mb, 8, 2, 3.0/8 + (1-1.0/8)*2},
		{"zero rate uses 10MB/s fallback", 100 * mb, 0, 0, 1, 10, 10},
		{"negative rate uses fallback", 100 * mb, 0, -5, 1, 10, 10},
		{"empty job", 0, 0, 10 * mb, 4, 0, 0},
		{"more workers than files", 3 * mb, mb, mb, 64, 1, 3.0/64 + (1-1.0/64)*1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi := ComputeMakespanBounds(tt.total, tt.largest, tt.rate, tt.workers)
			if math.Abs(lo-tt.wantLo) > 1e-9 || math.Abs(hi-tt.wantHi) > 1e-9 {
				t.Fatalf("bounds = (%v, %v), want (%v, %v)", lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}

// FuzzComputeMakespanBounds checks Graham's bound ordering: for any valid
// workload (largest <= total) low <= high <= 2*low, and adding workers never
// increases the lower bound.
func FuzzComputeMakespanBounds(f *testing.F) {
	f.Add(int64(100), int64(20), 10.0, 4)
	f.Add(int64(1), int64(1), 1.0, 1)
	f.Add(int64(1<<40), int64(1<<30), 1e6, 128)
	f.Fuzz(func(t *testing.T, total, largest int64, rate float64, workers int) {
		if total < 0 || largest < 0 || largest > total || math.IsNaN(rate) || math.IsInf(rate, 0) || workers > 1<<16 {
			return
		}
		lo, hi := ComputeMakespanBounds(total, largest, rate, workers)
		if lo < 0 || hi < 0 || math.IsNaN(lo) || math.IsNaN(hi) {
			t.Fatalf("invalid bounds (%v, %v)", lo, hi)
		}
		eps := 1e-9 * math.Max(1, hi)
		if lo > hi+eps {
			t.Fatalf("low %v > high %v", lo, hi)
		}
		if hi > 2*lo+eps {
			t.Fatalf("Graham: high %v must be <= 2*low %v", hi, 2*lo)
		}
		if workers >= 1 {
			lo2, _ := ComputeMakespanBounds(total, largest, rate, workers+1)
			if lo2 > lo+eps {
				t.Fatalf("more workers increased low bound: %v -> %v", lo, lo2)
			}
		}
	})
}

func TestEstimateJob_DotDirectoryRootIsWalked(t *testing.T) {
	// Regression: the hidden-directory filter also matched the walk root, so
	// `--docs ./.github` (or any dot-named root) estimated zero files.
	root := filepath.Join(t.TempDir(), ".github")
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "ci.yml"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nested hidden dirs are still skipped.
	if err := os.MkdirAll(filepath.Join(root, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cache", "x"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	est, err := EstimateJob(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if est.FileCount != 1 || est.TotalBytes != 100 {
		t.Fatalf("expected 1 file / 100 bytes, got %d / %d", est.FileCount, est.TotalBytes)
	}
}

func TestEstimateJob_LargeFileSampleAndEmptyDir(t *testing.T) {
	dir := t.TempDir()
	est, err := EstimateJob(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if est.FileCount != 0 || est.TotalBytes != 0 || est.EstimatedSecHigh != 0 || est.Workers < 1 {
		t.Fatalf("empty dir estimate: %+v", est)
	}

	// A sparse 64 MiB file exceeds the 4 MiB sample cap; the estimate must
	// still account for its full size (and only sample a prefix of it).
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	est, err = EstimateJob(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	if est.TotalBytes != 64<<20 || est.LargestFileBytes != 64<<20 || est.FileCount != 1 {
		t.Fatalf("unexpected estimate: %+v", est)
	}
	if est.SampledRate <= 0 {
		t.Fatalf("sampled rate must be positive: %v", est.SampledRate)
	}
	// With a single file, low == largest/rate and high adds (1-1/W)*max.
	maxWork := float64(est.LargestFileBytes) / est.SampledRate
	if math.Abs(est.EstimatedSecLow-maxWork) > 1e-9*math.Max(1, maxWork) {
		t.Fatalf("single-file low bound %v != max work %v", est.EstimatedSecLow, maxWork)
	}
}

func TestEstimateJob_MissingDir(t *testing.T) {
	est, err := EstimateJob(filepath.Join(t.TempDir(), "nope"), 1)
	// WalkDir callback swallows the root error; the estimate is empty.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.FileCount != 0 {
		t.Fatalf("expected zero files, got %d", est.FileCount)
	}
}

func TestEvaluateDecisionGate_Table(t *testing.T) {
	est := &JobEstimate{FileCount: 10, TotalBytes: 1000, EstimatedSecLow: 50, EstimatedSecHigh: 120}
	tests := []struct {
		name       string
		maxMinutes float64
		approve    string
		triggered  bool
		action     GateAction
	}{
		{"under threshold", 5, "", false, GateActionProceed},
		{"exactly at threshold does not trigger", 2, "", false, GateActionProceed},
		{"over threshold triggers exit 4", 1, "", true, ""},
		{"zero max disables gate", 0, "", false, GateActionProceed},
		{"negative max disables gate", -1, "", false, GateActionProceed},
		{"approve proceed", 1, "proceed", false, GateActionProceed},
		{"approve case and spaces", 1, "  YES ", false, GateActionProceed},
		{"approve true", 1, "true", false, GateActionProceed},
		{"approve fast", 1, "Fast", false, GateActionFast},
		{"approve cancel", 1, "cancel", false, GateActionCancel},
		{"approve no", 1, "no", false, GateActionCancel},
		{"approve abort", 1, "ABORT", false, GateActionCancel},
		{"unknown approve falls through to gate", 1, "maybe", true, ""},
		{"approval wins even without threshold", 0, "cancel", false, GateActionCancel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := EvaluateDecisionGate(est, tt.maxMinutes, tt.approve)
			if res.Triggered != tt.triggered || res.Action != tt.action {
				t.Fatalf("got triggered=%v action=%q, want %v %q", res.Triggered, res.Action, tt.triggered, tt.action)
			}
			if res.Triggered {
				if res.ExitCode != 4 || res.ThresholdSeconds != tt.maxMinutes*60 || len(res.Options) != 3 {
					t.Fatalf("triggered result incomplete: %+v", res)
				}
				if res.FileCount != est.FileCount || res.EstimatedSecondsHigh != est.EstimatedSecHigh {
					t.Fatalf("triggered result does not echo estimate: %+v", res)
				}
			}
		})
	}
}
