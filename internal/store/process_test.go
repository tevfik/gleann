package store

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The process tests re-exec the test binary as a child (the standard Go
// helper-process pattern). TestHelperProcess is a no-op in a normal run; the
// children select their role through GLEANN_STORE_HELPER.
const (
	helperEnv     = "GLEANN_STORE_HELPER"
	helperDBEnv   = "GLEANN_STORE_DB"
	helperLockEnv = "GLEANN_STORE_LOCK"
	helperIDEnv   = "GLEANN_STORE_WRITER_ID"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return // normal test run
	}
	dbPath := os.Getenv(helperDBEnv)
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch mode {
	case "kill-mid-apply":
		// Open, begin a large ApplySource, and let the parent kill the
		// process before the transaction commits.
		s, err := Open(dbPath, Options{})
		if err != nil {
			fail(err)
		}
		const n = 40000
		ps := make([]NewPassage, n)
		for i := range ps {
			text := fmt.Sprintf("chunk %d %s", i, strings.Repeat("lorem ipsum dolor sit amet ", 80))
			ps[i] = NewPassage{Text: text, Tokens: text}
		}
		fmt.Println("begin")
		if _, err := s.ApplySource(context.Background(), SourceDelta{
			Source:   "big.go",
			File:     FileRow{Hash: "new"},
			Passages: ps,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		fmt.Println("committed")
		s.Close()
		os.Exit(0)

	case "writer":
		id := os.Getenv(helperIDEnv)
		s, err := Open(dbPath, Options{})
		if err != nil {
			fail(err)
		}
		for f := 0; f < 3; f++ {
			source := fmt.Sprintf("w%s-file%d.go", id, f)
			ps := make([]NewPassage, 40)
			for i := range ps {
				text := fmt.Sprintf("writer %s file %d chunk %d", id, f, i)
				ps[i] = NewPassage{Text: text, Tokens: text}
			}
			if _, err := s.ApplySource(context.Background(), SourceDelta{
				Source:   source,
				File:     FileRow{Hash: source},
				Passages: ps,
			}); err != nil {
				fail(err)
			}
		}
		if err := s.Close(); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "reader":
		s, err := Open(dbPath, Options{})
		if err != nil {
			fail(err)
		}
		ctx := context.Background()
		for i := 0; i < 400; i++ {
			if _, err := s.Count(ctx); err != nil {
				fail(err)
			}
			if err := s.ForEach(ctx, func(Passage) error { return nil }); err != nil {
				fail(err)
			}
			time.Sleep(2 * time.Millisecond)
		}
		if err := s.Close(); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "opener":
		// Wait for the start file so all openers hit the new database together.
		for {
			if _, err := os.Stat(dbPath + ".go"); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		s, err := Open(dbPath, Options{})
		if err != nil {
			fail(err)
		}
		if err := s.Close(); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "lock-holder":
		l, err := AcquireLock(os.Getenv(helperLockEnv))
		if err != nil {
			fail(err)
		}
		fmt.Println("locked")
		time.Sleep(time.Hour) // the parent kills us
		l.Release()
		os.Exit(0)

	default:
		fail(fmt.Errorf("unknown helper mode %q", mode))
	}
}

func helperCmd(t *testing.T, mode, dbPath string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), helperEnv+"="+mode, helperDBEnv+"="+dbPath)
	cmd.Env = append(cmd.Env, extraEnv...)
	return cmd
}

// waitLine reads child stdout until the wanted line appears, with a timeout.
func waitLine(t *testing.T, stdout io.Reader, want string, timeout time.Duration) {
	t.Helper()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("child exited before printing %q", want)
			}
			if line == want {
				return
			}
		case <-deadline:
			t.Fatalf("child did not print %q within %s", want, timeout)
		}
	}
}

// TestKillMidTransaction kills a child in the middle of ApplySource and
// checks that reopening sees exactly the old data (RFC 003 section 7.1).
func TestKillMidTransaction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := Open(dbPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	applyTexts(t, s, "old.go", []string{"old one", "old two", "old three"})
	// big.go already has 1,200 passages; the child replaces them, so the kill
	// lands in a delete-and-insert of an existing large source (RFC 7.1).
	oldBig := make([]string, 1200)
	for i := range oldBig {
		oldBig[i] = fmt.Sprintf("prior chunk %d", i)
	}
	applyTexts(t, s, "big.go", oldBig)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := helperCmd(t, "kill-mid-apply", dbPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitLine(t, stdout, "begin", 90*time.Second)
	// The delta is 40,000 passages with FTS tokens (seconds of work); the
	// kill lands well inside the transaction.
	time.Sleep(250 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(stdout)
	cmd.Wait()
	if strings.Contains(string(rest), "committed") {
		t.Fatal("child committed before the kill; the delta is too small for this machine")
	}

	s2, err := Open(dbPath, Options{})
	if err != nil {
		t.Fatalf("reopen after kill: %v", err)
	}
	defer s2.Close()
	n, err := s2.Count(ctx)
	if err != nil || n != 3+1200 {
		t.Fatalf("Count after kill = %d, %v; want the 1,203 old passages", n, err)
	}
	files, err := s2.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if files["old.go"].Hash != "hash-old.go" {
		t.Errorf("manifest changed after kill: %+v", files["old.go"])
	}
	if files["big.go"].Hash != "hash-big.go" {
		t.Errorf("big.go manifest row changed after kill: %+v", files["big.go"])
	}
	if lexCount(t, s2, "prior") != 1200 {
		t.Error("big.go lex rows lost after kill")
	}
	if lexCount(t, s2, "old") != 3 {
		t.Error("old lex rows lost after kill")
	}
	if lexCount(t, s2, "lorem") != 0 {
		t.Error("uncommitted lex rows survived the kill")
	}
}

// TestTwoWritersOneReader re-runs the spike's writers test as a regular
// test: two writer processes and one reader on the same database, no
// errors, all writes present (RFC 003 sections 1.1, 7.1).
func TestTwoWritersOneReader(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idx", "idx.db")
	w1 := helperCmd(t, "writer", dbPath, helperIDEnv+"=A")
	w2 := helperCmd(t, "writer", dbPath, helperIDEnv+"=B")
	r := helperCmd(t, "reader", dbPath)
	var out1, out2, outR strings.Builder
	w1.Stderr, w2.Stderr, r.Stderr = &out1, &out2, &outR
	for _, cmd := range []*exec.Cmd{w1, w2, r} {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	if err := w1.Wait(); err != nil {
		t.Fatalf("writer A: %v\n%s", err, out1.String())
	}
	if err := w2.Wait(); err != nil {
		t.Fatalf("writer B: %v\n%s", err, out2.String())
	}
	if err := r.Wait(); err != nil {
		t.Fatalf("reader: %v\n%s", err, outR.String())
	}

	s, err := Open(dbPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	n, err := s.Count(ctx)
	if err != nil || n != 240 {
		t.Fatalf("Count = %d, %v; want 240 (2 writers x 3 sources x 40 passages)", n, err)
	}
	files, err := s.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("Files has %d rows, want 6", len(files))
	}
	perSource := make(map[string]int)
	if err := s.ForEach(ctx, func(p Passage) error {
		perSource[p.Source]++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for source, count := range perSource {
		if count != 40 {
			t.Errorf("source %s has %d passages, want 40", source, count)
		}
	}
}

// TestLockHeldByChild checks that a lock held by another process makes
// AcquireLock fail fast with a readable message, and that killing the holder
// releases the lock (RFC 003 section 3.6: no stale locks).
func TestLockHeldByChild(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "idx.lock")
	cmd := helperCmd(t, "lock-holder", "", helperLockEnv+"="+lockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitLine(t, stdout, "locked", 30*time.Second)

	start := time.Now()
	_, err = AcquireLock(lockPath)
	if err == nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("AcquireLock succeeded while a child holds the lock")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("AcquireLock waited %s; it must fail fast", elapsed)
	}
	if !strings.Contains(err.Error(), "held by another process") {
		t.Errorf("lock error not readable: %v", err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	// The OS released the lock when the child died.
	l, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("AcquireLock after holder was killed: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// Several processes opening a brand-new database at the same moment must all
// succeed: the schema is created once, and the others see it instead of
// failing on "table already exists".
func TestConcurrentFirstOpen(t *testing.T) {
	for round := 0; round < 5; round++ {
		dbPath := filepath.Join(t.TempDir(), "idx.db")
		const n = 8
		cmds := make([]*exec.Cmd, n)
		outs := make([]*strings.Builder, n)
		for i := range cmds {
			cmds[i] = helperCmd(t, "opener", dbPath)
			outs[i] = &strings.Builder{}
			cmds[i].Stderr = outs[i]
			if err := cmds[i].Start(); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(200 * time.Millisecond) // let every child reach the wait loop
		if err := os.WriteFile(dbPath+".go", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		for i, c := range cmds {
			if err := c.Wait(); err != nil {
				t.Errorf("round %d opener %d: %v: %s", round, i, err, outs[i].String())
			}
		}
	}
}
