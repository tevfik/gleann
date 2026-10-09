package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func runCmdBuildWithMockExit(args []string) (stdout string, stderr string, exitCode int, err error) {
	origExit := exitFunc
	defer func() { exitFunc = origExit }()

	lastCode := 0
	exitFunc = func(code int) {
		lastCode = code
		panic(fmt.Sprintf("exit_%d", code))
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				// caught exit panic
			}
		}()
		cmdBuild(args)
	}()

	_ = wOut.Close()
	_ = wErr.Close()

	var bufOut, bufErr bytes.Buffer
	_, _ = io.Copy(&bufOut, rOut)
	_, _ = io.Copy(&bufErr, rErr)

	return bufOut.String(), bufErr.String(), lastCode, nil
}

func TestIntegration_BuildDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	// Create sample files
	for i := 0; i < 5; i++ {
		fn := filepath.Join(tmpDir, fmt.Sprintf("sample_%d.go", i))
		content := fmt.Sprintf("package sample\nfunc SampleFunc%d() string { return %q }\n", i, strings.Repeat("A", 1024))
		_ = os.WriteFile(fn, []byte(content), 0644)
	}

	t.Run("HumanReadable", func(t *testing.T) {
		stdout, stderr, exitCode, err := runCmdBuildWithMockExit([]string{
			"test-idx",
			"--docs", tmpDir,
			"--dry-run",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("expected exitCode 0, got %d. stderr: %s", exitCode, stderr)
		}
		if !strings.Contains(stdout, "Pre-flight Job Estimate") {
			t.Errorf("expected Pre-flight Job Estimate in stdout, got: %s", stdout)
		}
		if !strings.Contains(stdout, "Graham Makespan Bounds") {
			t.Errorf("expected Graham Makespan Bounds in stdout, got: %s", stdout)
		}
	})

	t.Run("JSONOutput", func(t *testing.T) {
		stdout, stderr, exitCode, err := runCmdBuildWithMockExit([]string{
			"test-idx",
			"--docs", tmpDir,
			"--dry-run",
			"--json",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("expected exitCode 0, got %d. stderr: %s", exitCode, stderr)
		}

		var est gleann.JobEstimate
		if err := json.Unmarshal([]byte(stdout), &est); err != nil {
			t.Fatalf("failed to parse JSON estimate from stdout %q: %v", stdout, err)
		}
		if est.FileCount != 5 {
			t.Errorf("expected 5 files, got %d", est.FileCount)
		}
		if est.TotalBytes <= 0 {
			t.Errorf("expected totalBytes > 0, got %d", est.TotalBytes)
		}
		if est.EstimatedSecHigh <= 0 {
			t.Errorf("expected EstimatedSecHigh > 0, got %f", est.EstimatedSecHigh)
		}
	})
}

func TestIntegration_DecisionGate_ThresholdExceeded_Exit4(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 3; i++ {
		fn := filepath.Join(tmpDir, fmt.Sprintf("file_%d.txt", i))
		_ = os.WriteFile(fn, []byte(strings.Repeat("data ", 500)), 0644)
	}

	// Set threshold very low: 0.000001 minutes (0.00006s) so Graham high bound exceeds it
	stdout, _, exitCode, err := runCmdBuildWithMockExit([]string{
		"test-idx",
		"--docs", tmpDir,
		"--max-minutes", "0.000001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 4 {
		t.Fatalf("expected exitCode 4, got %d. stdout: %s", exitCode, stdout)
	}

	var gate gleann.DecisionGateResult
	if err := json.Unmarshal([]byte(stdout), &gate); err != nil {
		t.Fatalf("expected valid JSON decision payload, got: %s, err: %v", stdout, err)
	}
	if !gate.Triggered {
		t.Errorf("expected gate.Triggered to be true")
	}
	if gate.ExitCode != 4 {
		t.Errorf("expected gate.ExitCode to be 4, got %d", gate.ExitCode)
	}
	if len(gate.Options) != 3 {
		t.Errorf("expected 3 options [proceed, fast, cancel], got %v", gate.Options)
	}
}

func TestIntegration_DecisionGate_ApproveCancel_Exit0(t *testing.T) {
	tmpDir := t.TempDir()
	fn := filepath.Join(tmpDir, "file.txt")
	_ = os.WriteFile(fn, []byte("some content"), 0644)

	stdout, stderr, exitCode, err := runCmdBuildWithMockExit([]string{
		"test-idx",
		"--docs", tmpDir,
		"--max-minutes", "0.000001",
		"--approve", "cancel",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exitCode 0 on cancel, got %d. stderr: %s", exitCode, stderr)
	}
	if !strings.Contains(stdout, "Indexing cancelled") {
		t.Errorf("expected 'Indexing cancelled' in stdout, got: %s", stdout)
	}
}
