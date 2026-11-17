package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The process tests re-exec the test binary as a child (the standard Go
// helper-process pattern, as internal/store/process_test.go). TestHelperProcess
// is a no-op in a normal run; the children select their role through
// GLEANN_MEMORY_HELPER.
const (
	memHelperEnv     = "GLEANN_MEMORY_HELPER"
	memHelperDBEnv   = "GLEANN_MEMORY_DB"
	memHelperKillEnv = "GLEANN_MEMORY_KILL_STEP"
	memHelperIDEnv   = "GLEANN_MEMORY_WRITER_ID"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(memHelperEnv)
	if mode == "" {
		return // normal test run
	}
	legacyPath := os.Getenv(memHelperDBEnv)
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch mode {
	case "migrate-kill":
		// Run the migration and block inside the notify hook at the step
		// the parent wants to kill; the OS releases the migration lock
		// when the process dies.
		killStep := 0
		fmt.Sscan(os.Getenv(memHelperKillEnv), &killStep)
		notify := func(step int) {
			fmt.Printf("step%d\n", step)
			if step == killStep {
				select {} // the parent kills us here
			}
		}
		if err := migrateFromBbolt(legacyPath, sqlitePathFor(legacyPath), notify); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "opener":
		// Wait for the start file so all openers hit the legacy database
		// at the same moment.
		for {
			if _, err := os.Stat(legacyPath + ".go"); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		s, err := OpenStore(legacyPath)
		if err != nil {
			fail(err)
		}
		if err := s.Close(); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "writer":
		id := os.Getenv(memHelperIDEnv)
		s, err := OpenStore(legacyPath)
		if err != nil {
			fail(err)
		}
		for i := 0; i < 50; i++ {
			if err := s.Add(&Block{
				Tier:    TierLong,
				Label:   "mem04",
				Content: fmt.Sprintf("writer %s note %d", id, i),
				Source:  "test",
			}); err != nil {
				fail(err)
			}
		}
		if err := s.Close(); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "dedup-writer":
		s, err := OpenStore(legacyPath)
		if err != nil {
			fail(err)
		}
		for i := 0; i < 25; i++ {
			if err := s.Add(&Block{
				ID:      "shared-dedup",
				Tier:    TierLong,
				Label:   "fact",
				Content: "the same repeated fact",
				Source:  "test",
			}); err != nil {
				fail(err)
			}
		}
		if err := s.Close(); err != nil {
			fail(err)
		}
		os.Exit(0)

	case "bbolt-holder":
		// An old binary holding the exclusive bbolt lock on memory.db.
		db, err := bolt.Open(legacyPath, 0o600, &bolt.Options{Timeout: 5 * time.Second})
		if err != nil {
			fail(err)
		}
		fmt.Println("locked")
		time.Sleep(time.Hour) // the parent kills us
		db.Close()
		os.Exit(0)

	default:
		fail(fmt.Errorf("unknown helper mode %q", mode))
	}
}

func memHelperCmd(t *testing.T, mode, legacyPath string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "-test.timeout=180s")
	cmd.Env = append(os.Environ(), memHelperEnv+"="+mode, memHelperDBEnv+"="+legacyPath)
	cmd.Env = append(cmd.Env, extraEnv...)
	return cmd
}

// memWaitLine reads child stdout until the wanted line appears, with a timeout.
func memWaitLine(t *testing.T, stdout io.Reader, want string, timeout time.Duration) {
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

// TestMigrateKillAtEachStep kills the migration at each of its five steps
// (RFC 003 sections 6.4 Memory, 7.1): the intermediate state is the one the
// RFC describes, and the next open ends in a complete memory.sqlite with all
// fixture data, the .v1 backup and the guard.
func TestMigrateKillAtEachStep(t *testing.T) {
	for step := 1; step <= 5; step++ {
		t.Run(fmt.Sprintf("step%d", step), func(t *testing.T) {
			dir := t.TempDir()
			legacyPath := filepath.Join(dir, "memory.db")
			blocks, summaries, meta := migrationFixture(40)
			writeBboltFixture(t, legacyPath, blocks, summaries, meta)
			sqlitePath := sqlitePathFor(legacyPath)

			cmd := memHelperCmd(t, "migrate-kill", legacyPath, fmt.Sprintf("%s=%d", memHelperKillEnv, step))
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			memWaitLine(t, stdout, fmt.Sprintf("step%d", step), 60*time.Second)
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			io.ReadAll(stdout)
			cmd.Wait()

			// The intermediate state matches the RFC's crash analysis.
			switch step {
			case 1:
				if fileExists(sqlitePath) || fileExists(sqlitePath+".tmp") {
					t.Fatal("step 1 kill left a .tmp or memory.sqlite")
				}
			case 2, 3:
				if fileExists(sqlitePath) {
					t.Fatal("memory.sqlite exists before the step 4 rename")
				}
				if !fileExists(sqlitePath + ".tmp") {
					t.Fatal("killed copy left no .tmp to discard")
				}
			case 4:
				if !fileExists(sqlitePath) {
					t.Fatal("memory.sqlite missing after the step 4 rename")
				}
				if kind, err := classifyLegacy(legacyPath); err != nil || kind != legacyBbolt {
					t.Fatalf("legacy file after step 4 kill = %v, %v; want the bbolt original", kind, err)
				}
			case 5:
				if !fileExists(sqlitePath) || fileExists(legacyPath) {
					t.Fatal("step 5 kill: want memory.sqlite present and memory.db renamed")
				}
				if !fileExists(legacyPath + ".v1") {
					t.Fatal("step 5 kill: .v1 backup missing")
				}
			}

			// The next open redoes or completes the migration.
			s, err := OpenStore(legacyPath)
			if err != nil {
				t.Fatalf("OpenStore after kill at step %d: %v\nchild stderr: %s", step, err, stderr.String())
			}
			defer s.Close()
			assertMigratedStore(t, s, legacyPath, blocks, summaries, meta)
		})
	}
}

// TestMigrateKillMidCopy kills the child while the block copy transaction is
// running: the uncommitted .tmp is discarded and the retry copies everything.
func TestMigrateKillMidCopy(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(20000)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)

	// Block at step 3, but kill shortly after step 2 so the kill lands
	// inside the copy, not after it.
	cmd := memHelperCmd(t, "migrate-kill", legacyPath, memHelperKillEnv+"=3")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	memWaitLine(t, stdout, "step2", 60*time.Second)
	time.Sleep(150 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(stdout)
	cmd.Wait()
	if strings.Contains(string(rest), "step3") {
		t.Fatal("child finished the copy before the kill; the fixture is too small for this machine")
	}

	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("OpenStore after mid-copy kill: %v\nchild stderr: %s", err, stderr.String())
	}
	defer s.Close()
	var n int
	if err := s.reader.QueryRow(`SELECT COUNT(*) FROM blocks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(blocks) {
		t.Fatalf("blocks after mid-copy kill retry = %d, want %d", n, len(blocks))
	}
	assertMigratedStore(t, s, legacyPath, blocks, summaries, meta)
}

// TestTwoProcessesMigrateOnce starts several processes against the same
// legacy memory.db at once: all opens succeed and the migration runs once —
// no duplicated rows, one .v1 backup, the guard in place (RFC 003 7.1).
func TestTwoProcessesMigrateOnce(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(60)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)

	const n = 4
	cmds := make([]*exec.Cmd, n)
	outs := make([]*strings.Builder, n)
	for i := range cmds {
		cmds[i] = memHelperCmd(t, "opener", legacyPath)
		outs[i] = &strings.Builder{}
		cmds[i].Stderr = outs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond) // let every child reach the wait loop
	if err := os.WriteFile(legacyPath+".go", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("opener %d: %v: %s", i, err, outs[i].String())
		}
	}

	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.reader.QueryRow(`SELECT COUNT(*) FROM blocks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(blocks) {
		t.Fatalf("blocks = %d, want %d (migration ran twice or lost rows)", count, len(blocks))
	}
	if got, err := s.metaValue("migrated_blocks"); err != nil || got != fmt.Sprint(len(blocks)) {
		t.Fatalf("migrated_blocks = %q, %v; want %d", got, err, len(blocks))
	}
	for _, id := range []string{blocks[0].ID, blocks[len(blocks)-1].ID} {
		if _, err := s.Get(id); err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
	}
	if _, err := os.Stat(legacyPath + ".v1"); err != nil {
		t.Fatalf(".v1 backup missing: %v", err)
	}
	guard, err := os.ReadFile(legacyPath)
	if err != nil || string(guard) != guardText {
		t.Fatalf("guard missing or wrong: %v", err)
	}
	if fileExists(sqlitePathFor(legacyPath) + ".tmp") {
		t.Fatal("stale .tmp left behind")
	}
}

// TestFiveWriterProcessesMEM04 is MEM-04 (RFC 003 sections 7.3, 8 step 6):
// five concurrent writer processes, 50 Adds each, no errors, 250 blocks.
func TestFiveWriterProcessesMEM04(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")

	const writers = 5
	cmds := make([]*exec.Cmd, writers)
	outs := make([]*strings.Builder, writers)
	for i := range cmds {
		cmds[i] = memHelperCmd(t, "writer", legacyPath, fmt.Sprintf("%s=%c", memHelperIDEnv, 'A'+i))
		outs[i] = &strings.Builder{}
		cmds[i].Stderr = outs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("writer %c: %v\n%s", 'A'+i, err, outs[i].String())
		}
	}

	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	blocks, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != writers*50 {
		t.Fatalf("blocks = %d, want %d", len(blocks), writers*50)
	}
	perWriter := make(map[string]int)
	for _, b := range blocks {
		perWriter[strings.SplitN(b.Content, " ", 3)[1]]++
	}
	for i := 0; i < writers; i++ {
		id := string(rune('A' + i))
		if perWriter[id] != 50 {
			t.Errorf("writer %s has %d blocks, want 50", id, perWriter[id])
		}
	}
	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.LongTermCount != writers*50 {
		t.Fatalf("Stats.LongTermCount = %d, want %d", stats.LongTermCount, writers*50)
	}
}

// TestConcurrentDedupOneTransaction checks RFC 003 section 3.5's last bullet:
// Add's read-then-write deduplication runs in one BEGIN IMMEDIATE transaction,
// so two processes reinforcing the same block lose no confirms.
func TestConcurrentDedupOneTransaction(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")

	const writers = 2
	cmds := make([]*exec.Cmd, writers)
	outs := make([]*strings.Builder, writers)
	for i := range cmds {
		cmds[i] = memHelperCmd(t, "dedup-writer", legacyPath)
		outs[i] = &strings.Builder{}
		cmds[i].Stderr = outs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("dedup writer %d: %v\n%s", i, err, outs[i].String())
		}
	}

	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.reader.QueryRow(`SELECT COUNT(*) FROM blocks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("blocks = %d, want 1 (dedup by ID)", count)
	}
	b, err := s.Get("shared-dedup")
	if err != nil {
		t.Fatal(err)
	}
	// 50 Adds: one insert plus 49 reinforcements, none lost to a race.
	if b.Confirms != writers*25-1 {
		t.Fatalf("Confirms = %d, want %d (read-then-write raced)", b.Confirms, writers*25-1)
	}
}

// TestMigrateLockedByRunningProcess checks RFC 003 section 6.4 Memory step 2:
// while an old process holds the bbolt lock, the open fails with the existing
// "locked by another process" message and writes nothing; after the holder
// dies, the migration proceeds.
func TestMigrateLockedByRunningProcess(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(12)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd := memHelperCmd(t, "bbolt-holder", legacyPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	memWaitLine(t, stdout, "locked", 30*time.Second)

	_, err = OpenStore(legacyPath)
	if err == nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("OpenStore succeeded while another process holds the bbolt lock")
	}
	if !strings.Contains(err.Error(), "locked by another process") {
		t.Fatalf("error = %v, want the existing locked message", err)
	}
	if fileExists(sqlitePathFor(legacyPath)) || fileExists(sqlitePathFor(legacyPath)+".tmp") {
		t.Fatal("locked open created SQLite files")
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("legacy file changed by the locked open: %v", err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	// The OS released the bbolt lock when the holder died.
	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("OpenStore after holder died: %v", err)
	}
	defer s.Close()
	assertMigratedStore(t, s, legacyPath, blocks, summaries, meta)
}
