package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestE2E_DecisionGateSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess e2e test in short mode")
	}

	tmpDir := t.TempDir()
	docDir := filepath.Join(tmpDir, "docs")
	_ = os.MkdirAll(docDir, 0755)

	for i := 0; i < 5; i++ {
		fn := filepath.Join(docDir, fmt.Sprintf("sample_%d.go", i))
		content := fmt.Sprintf("package sample\n// Function %d\nfunc F%d() {\n%s\n}\n", i, i, strings.Repeat("\tprintln(1)\n", 2500))
		_ = os.WriteFile(fn, []byte(content), 0644)
	}

	// Build a temporary binary of cmd/gleann once for e2e tests
	binPath := filepath.Join(tmpDir, "gleann_gate_test_bin")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build test binary: %v\nOutput: %s", err, string(out))
	}

	t.Run("Subprocess_DecisionGate_Exit4", func(t *testing.T) {
		cmd := exec.Command(binPath, "index", "build", "e2e-gate-test", "--docs", docDir, "--max-minutes", "0.00001")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected command to exit with error code 4, but succeeded! stdout: %s", stdout.String())
		}

		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("expected ExitError, got %T: %v", err, err)
		}
		if exitErr.ExitCode() != 4 {
			t.Fatalf("expected exit code 4, got %d. stdout: %s, stderr: %s", exitErr.ExitCode(), stdout.String(), stderr.String())
		}

		var gate gleann.DecisionGateResult
		if err := json.Unmarshal(stdout.Bytes(), &gate); err != nil {
			t.Fatalf("failed to unmarshal JSON gate output: %v. stdout: %s", err, stdout.String())
		}
		if !gate.Triggered {
			t.Errorf("expected gate.Triggered to be true")
		}
		if gate.ExitCode != 4 {
			t.Errorf("expected gate.ExitCode == 4, got %d", gate.ExitCode)
		}
	})

	t.Run("Subprocess_DryRun_JSON_Exit0", func(t *testing.T) {
		cmd := exec.Command(binPath, "index", "build", "e2e-gate-test", "--docs", docDir, "--dry-run", "--json")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("dry-run failed with %v. stderr: %s", err, stderr.String())
		}

		var est gleann.JobEstimate
		if err := json.Unmarshal(stdout.Bytes(), &est); err != nil {
			t.Fatalf("failed to unmarshal dry-run JSON: %v. stdout: %s", err, stdout.String())
		}
		if est.FileCount != 5 {
			t.Errorf("expected 5 files, got %d", est.FileCount)
		}
	})

	t.Run("Subprocess_ApproveCancel_Exit0", func(t *testing.T) {
		cmd := exec.Command(binPath, "index", "build", "e2e-gate-test", "--docs", docDir, "--max-minutes", "0.0000001", "--approve", "cancel")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("approve cancel failed with %v. stderr: %s", err, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Indexing cancelled") {
			t.Errorf("expected cancellation message, got: %s", stdout.String())
		}
	})

	t.Run("Subprocess_DryRun_HumanReadable", func(t *testing.T) {
		cmd := exec.Command(binPath, "index", "build", "e2e-gate-test", "--docs", docDir, "--dry-run")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("dry-run failed with %v. stderr: %s", err, stderr.String())
		}
		outStr := stdout.String()
		if !strings.Contains(outStr, "Pre-flight Job Estimate") {
			t.Errorf("expected 'Pre-flight Job Estimate', got: %s", outStr)
		}
		if !strings.Contains(outStr, "Graham Makespan Bounds") {
			t.Errorf("expected 'Graham Makespan Bounds', got: %s", outStr)
		}
	})
}
