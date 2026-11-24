package gleann

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	metaHelperEnv  = "GLEANN_META_LOCK_HELPER_DIR"
	metaHelperHold = 700 * time.Millisecond
)

// A child process holds the metadata lock of an index (as a CLI rebuild would)
// for a while; an update in this process waits for it and then applies on top
// of what the child wrote, so neither edit is lost.
func TestUpdateIndexMetaWaitsForOtherProcess(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndexMetaForTest(dir, "idx"); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestMetaLockHelperProcess$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), metaHelperEnv+"="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	sc := bufio.NewScanner(out)
	for sc.Scan() && sc.Text() != "locked" {
	}

	start := time.Now()
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Description = "from parent" }); err != nil {
		t.Fatalf("update: %v", err)
	}
	if waited := time.Since(start); waited < metaHelperHold/2 {
		t.Fatalf("update did not wait for the other process (took %s)", waited)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if m.Description != "from parent" || len(m.Tags) != 1 || m.Tags[0] != "from-child" {
		t.Fatalf("an edit was lost: description=%q tags=%v", m.Description, m.Tags)
	}
}

// UpdateIndexMetaForTest writes a minimal metadata file.
func UpdateIndexMetaForTest(dir, name string) error {
	return replaceIndexMeta(dir, name, func(*IndexMeta) (*IndexMeta, error) {
		return &IndexMeta{Name: name}, nil
	})
}

// TestMetaLockHelperProcess is the child: it is a no-op unless started by the
// test above.
func TestMetaLockHelperProcess(t *testing.T) {
	dir := os.Getenv(metaHelperEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	unlock, err := lockMeta(filepath.Join(dir, "idx", "idx.meta.json"))
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(metaHelperHold)
	// The child's own read-modify-write, done while it holds the lock.
	m, _ := GetIndexMeta(dir, "idx")
	m.Tags = []string{"from-child"}
	data, _ := jsonMarshalIndent(m)
	if err := writeMetaFile(filepath.Join(dir, "idx", "idx.meta.json"), "idx", data); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	unlock()
	os.Exit(0)
}

func jsonMarshalIndent(m *IndexMeta) ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

// An update of an index that does not exist is a not-exist error and leaves
// no lock file behind.
func TestUpdateIndexMetaMissingLeavesNoLockFile(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		err := UpdateIndexMeta(dir, "nope", func(*IndexMeta) {})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want not-exist", err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.lock")); len(left) != 0 {
		t.Fatalf("lock files left behind: %v", left)
	}
}
