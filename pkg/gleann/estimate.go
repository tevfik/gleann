package gleann

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GateAction defines approved actions for long-running indexing runs.
type GateAction string

const (
	GateActionProceed GateAction = "proceed"
	GateActionFast    GateAction = "fast"
	GateActionCancel  GateAction = "cancel"
)

// JobEstimate holds pre-flight indexing workload measurements and Graham makespan bounds.
type JobEstimate struct {
	FileCount        int     `json:"file_count"`
	TotalBytes       int64   `json:"total_bytes"`
	SampledRate      float64 `json:"sampled_rate_bytes_per_sec"`
	EstimatedSecLow  float64 `json:"estimated_seconds_low"`
	EstimatedSecHigh float64 `json:"estimated_seconds_high"`
	Workers          int     `json:"workers"`
	LargestFileBytes int64   `json:"largest_file_bytes"`
}

// DecisionGateResult holds the machine-readable decision gate status.
type DecisionGateResult struct {
	Triggered            bool       `json:"decision_required"`
	ExitCode             int        `json:"exit_code,omitempty"`
	Reason               string     `json:"reason,omitempty"`
	EstimatedSecondsLow  float64    `json:"estimated_seconds_low,omitempty"`
	EstimatedSecondsHigh float64    `json:"estimated_seconds_high,omitempty"`
	ThresholdSeconds     float64    `json:"threshold_seconds,omitempty"`
	FileCount            int        `json:"files,omitempty"`
	TotalBytes           int64      `json:"bytes,omitempty"`
	Options              []string   `json:"options,omitempty"`
	Hint                 string     `json:"hint,omitempty"`
	Action               GateAction `json:"action,omitempty"`
}

// ComputeMakespanBounds computes lower and upper makespan bounds using Graham's (1969) list-scheduling:
//
//	low  = max(totalWork / W, maxWork)
//	high = totalWork / W + (1 - 1/W) * maxWork
func ComputeMakespanBounds(totalBytes, largestFileBytes int64, rateBytesPerSec float64, workers int) (float64, float64) {
	if workers <= 0 {
		workers = 1
	}
	if rateBytesPerSec <= 0 {
		rateBytesPerSec = 10 * 1024 * 1024 // 10 MB/s conservative fallback
	}

	totalWork := float64(totalBytes) / rateBytesPerSec
	maxWork := float64(largestFileBytes) / rateBytesPerSec

	w := float64(workers)
	low := math.Max(totalWork/w, maxWork)
	high := (totalWork / w) + (1.0-1.0/w)*maxWork

	return low, high
}

// EstimateJob walks dir, measures local extraction rate, and computes Graham makespan bounds.
func EstimateJob(dir string, workers int) (*JobEstimate, error) {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers <= 0 {
		workers = 1
	}

	var totalBytes int64
	var largestFileBytes int64
	var fileCount int
	var sampleBytes int64
	var sampleFiles []string

	sampleCap := int64(4 * 1024 * 1024) // 4 MB sample to measure read rate

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		sz := info.Size()
		totalBytes += sz
		fileCount++
		if sz > largestFileBytes {
			largestFileBytes = sz
		}

		if sampleBytes < sampleCap {
			sampleFiles = append(sampleFiles, p)
			sampleBytes += sz
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	// Measure read rate on this machine
	measuredRate := float64(15 * 1024 * 1024) // 15 MB/s fallback
	if len(sampleFiles) > 0 {
		start := time.Now()
		var bytesRead int64
		for _, sf := range sampleFiles {
			if data, rErr := os.ReadFile(sf); rErr == nil {
				bytesRead += int64(len(data))
			}
		}
		elapsed := time.Since(start).Seconds()
		if elapsed > 0.001 && bytesRead > 0 {
			measuredRate = float64(bytesRead) / elapsed
		}
	}

	low, high := ComputeMakespanBounds(totalBytes, largestFileBytes, measuredRate, workers)

	return &JobEstimate{
		FileCount:        fileCount,
		TotalBytes:       totalBytes,
		SampledRate:      measuredRate,
		EstimatedSecLow:  low,
		EstimatedSecHigh: high,
		Workers:          workers,
		LargestFileBytes: largestFileBytes,
	}, nil
}

// EvaluateDecisionGate checks whether estimated job duration exceeds the threshold and formats options.
func EvaluateDecisionGate(est *JobEstimate, maxMinutes float64, approve string) DecisionGateResult {
	approve = strings.ToLower(strings.TrimSpace(approve))

	// Handle explicit approvals
	switch approve {
	case "proceed", "yes", "true":
		return DecisionGateResult{Action: GateActionProceed}
	case "fast":
		return DecisionGateResult{Action: GateActionFast}
	case "cancel", "no", "abort":
		return DecisionGateResult{Action: GateActionCancel}
	}

	thresholdSec := maxMinutes * 60.0
	if maxMinutes > 0 && est.EstimatedSecHigh > thresholdSec {
		return DecisionGateResult{
			Triggered:            true,
			ExitCode:             4,
			Reason:               "estimated_time_exceeds_threshold",
			EstimatedSecondsLow:  est.EstimatedSecLow,
			EstimatedSecondsHigh: est.EstimatedSecHigh,
			ThresholdSeconds:     thresholdSec,
			FileCount:            est.FileCount,
			TotalBytes:           est.TotalBytes,
			Options:              []string{"proceed", "fast", "cancel"},
			Hint:                 "Run with --approve proceed to index all, --approve fast for code-only, or --approve cancel to abort.",
		}
	}

	return DecisionGateResult{Action: GateActionProceed}
}
