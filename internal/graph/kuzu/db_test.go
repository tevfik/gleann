//go:build treesitter

package kuzu

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLooksCorrupt(t *testing.T) {
	for msg, want := range map[string]bool{
		"database file is corrupted":            true,
		"WAL checksum mismatch":                 true,
		"failed to open database with status 1": false, // what a database held by another process reports
		"IO exception: cannot read the file":    false,
	} {
		if got := looksCorrupt(errors.New(msg)); got != want {
			t.Errorf("looksCorrupt(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestIsLockError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("some random corruption error"), false},
		{errors.New("failed to acquire lock on file"), true},
		{errors.New("Resource temporarily unavailable"), true},
		{errors.New("database already open by another process"), true},
		{errors.New("permission denied"), true},
		{errors.New("access is denied"), true},
		{errors.New("database is busy"), true},
	}

	for _, tc := range cases {
		got := isLockError(tc.err)
		if got != tc.want {
			t.Errorf("isLockError(%v) = %v; want %v", tc.err, got, tc.want)
		}
	}
}

// TestHelperHoldDatabase runs in a child process: it opens the graph database
// named by GLEANN_TEST_HOLD_DB, reports readiness and holds it until stdin closes.
func TestHelperHoldDatabase(t *testing.T) {
	dir := os.Getenv("GLEANN_TEST_HOLD_DB")
	if dir == "" {
		t.Skip("helper process only")
	}
	db, err := Open(dir)
	if err != nil {
		fmt.Println("open-failed:", err)
		return
	}
	defer db.Close()
	if err := db.exec(`CREATE (:Symbol {fqn: "keep.Me", name: "Me", kind: "function", file: "a.go", line: 1, doc: "", weight: 1.0, is_test: false})`); err != nil {
		fmt.Println("seed-failed:", err)
		return
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// A database that another process holds must be reported, never moved aside
// as "corrupted": that used to replace a healthy graph with an empty one.
func TestOpenDatabaseHeldByAnotherProcessIsNotQuarantined(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "g_graph")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldDatabase$")
	cmd.Env = append(os.Environ(), "GLEANN_TEST_HOLD_DB="+dir)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Wait() }()
	sc := bufio.NewScanner(stdout)
	ready := false
	for sc.Scan() {
		if sc.Text() == "ready" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("helper did not open the database")
	}

	db, err := Open(dir)
	if err == nil {
		db.Close()
		t.Skip("kuzu allows a second process here; nothing to guard")
	}
	if !errors.Is(err, ErrOpenFailed) {
		t.Errorf("err = %v, want ErrOpenFailed", err)
	}
	if matches, _ := filepath.Glob(dir + ".corrupted-*"); len(matches) != 0 {
		t.Errorf("healthy database was quarantined: %v", matches)
	}
}
