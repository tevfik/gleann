package indexing

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// The cross-process lock tests re-exec the test binary as a child (the
// standard Go helper-process pattern, as internal/store/process_test.go and
// pkg/memory/migrate_process_test.go). The child holds the exclusive
// <IndexDir>/<name>.lock that Build, Sync, Repair and migrate now take for the
// whole operation (RFC 003 section 3.6); the parent checks that a write path
// fails fast with a readable message and leaves the index untouched, and that
// killing the holder frees the lock. TestHelperProcess is a no-op in a normal
// run; the child selects its role through GLEANN_INDEXING_LOCK_HELPER.
const (
	lockHelperEnv     = "GLEANN_INDEXING_LOCK_HELPER"
	lockHelperPathEnv = "GLEANN_INDEXING_LOCK_PATH"
	lockHelperDirEnv  = "GLEANN_INDEXING_LOCK_DIR"
	lockHelperNameEnv = "GLEANN_INDEXING_LOCK_NAME"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(lockHelperEnv)
	if mode == "" {
		return // normal test run
	}
	switch mode {
	case "lock-holder":
		// Hold the raw <name>.lock file, until the parent kills us; the OS
		// releases it on exit.
		l, err := store.AcquireLock(os.Getenv(lockHelperPathEnv))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("locked")
		time.Sleep(time.Hour)
		l.Release()
		os.Exit(0)
	case "sync-lock-holder":
		// Hold the index lock exactly as a running Sync does, through the
		// same lockIndex call, until the parent kills us. Before the fix
		// lockIndex took no cross-process lock, so this left <name>.lock
		// free for a concurrent migrate.
		unlock, err := lockIndex(context.Background(), os.Getenv(lockHelperDirEnv), os.Getenv(lockHelperNameEnv))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("locked")
		time.Sleep(time.Hour)
		unlock()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(1)
	}
}

// lockHolder is a child process holding an index lock; stop is idempotent so
// the test can release it early and t.Cleanup can still reap it.
type lockHolder struct {
	cmd  *exec.Cmd
	once sync.Once
}

func (h *lockHolder) stop() {
	h.once.Do(func() {
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
			_ = h.cmd.Wait()
		}
	})
}

func startLockHolder(t *testing.T, lockPath string) *lockHolder {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), lockHelperEnv+"=lock-holder", lockHelperPathEnv+"="+lockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &lockHolder{cmd: cmd}
	t.Cleanup(h.stop)
	waitLine(t, stdout, "locked", 30*time.Second)
	return h
}

// startSyncLockHolder starts a child that holds the index lock through
// lockIndex, the same call Sync makes for its whole operation.
func startSyncLockHolder(t *testing.T, indexDir, name string) *lockHolder {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "-test.timeout=120s")
	cmd.Env = append(os.Environ(),
		lockHelperEnv+"=sync-lock-holder",
		lockHelperDirEnv+"="+indexDir,
		lockHelperNameEnv+"="+name,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &lockHolder{cmd: cmd}
	t.Cleanup(h.stop)
	waitLine(t, stdout, "locked", 30*time.Second)
	return h
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

// snapshotDir hashes every file under dir so a test can prove a locked-out
// write left the index byte-identical. The lock file lives beside the index
// directory, not inside it, so it is not part of the snapshot.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := make(map[string]string)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		m[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestBuildSyncFailFastWhenLockedByOtherProcess is RFC 003 section 3.6 for the
// indexing write paths: while another process holds <name>.lock, Build and Sync
// fail fast with a message naming the index, modify nothing, and proceed once
// the holder dies. It covers both store formats (the lock is format-agnostic).
func TestBuildSyncFailFastWhenLockedByOtherProcess(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	name := "lockedidx"
	// A lexical-only build is fast and needs no embedder; the lock is taken
	// before any reading or embedding regardless.
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: name, DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatalf("initial build: %v", err)
	}
	idxDir := filepath.Join(env.cfg.IndexDir, name)
	before := snapshotDir(t, idxDir)

	// A change for Sync to apply once the lock is free.
	writeFile(t, filepath.Join(docs, "README.md"), "# Project\n\nA changed intro text about the project.\n")

	h := startLockHolder(t, filepath.Join(env.cfg.IndexDir, name+".lock"))

	start := time.Now()
	_, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: name, DocsDir: docs, InstantOnly: true})
	if err == nil {
		t.Fatal("Build succeeded while another process holds the index lock")
	}
	if !errors.Is(err, ErrIndexLocked) || !strings.Contains(err.Error(), "held by another process") || !strings.Contains(err.Error(), name) {
		t.Fatalf("Build lock error = %v; want ErrIndexLocked naming the index", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Build waited %s; it must fail fast", elapsed)
	}

	start = time.Now()
	_, err = Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: name, DocsDir: docs})
	if err == nil {
		t.Fatal("Sync succeeded while another process holds the index lock")
	}
	if !errors.Is(err, ErrIndexLocked) || !strings.Contains(err.Error(), "held by another process") || !strings.Contains(err.Error(), name) {
		t.Fatalf("Sync lock error = %v; want ErrIndexLocked naming the index", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Sync waited %s; it must fail fast", elapsed)
	}

	if after := snapshotDir(t, idxDir); !reflect.DeepEqual(before, after) {
		t.Errorf("locked-out Build/Sync modified the index:\nbefore=%v\nafter=%v", before, after)
	}

	// Killing the holder releases the OS lock; Sync now proceeds.
	h.stop()
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: name, DocsDir: docs}); err != nil {
		t.Fatalf("Sync after holder died: %v", err)
	}
}

// TestMigrateFailsFastWhenSyncHoldsLock checks that migrate/rollback/cleanup
// respect the same <name>.lock a running sync holds: while a child process
// holds it through lockIndex (Sync's own lock), MigrateIndex fails fast and
// switches nothing; the lock is free again once the holder dies. Before the
// fix Sync took no cross-process lock, so a migrate could rename the store
// under a running sync — this test fails on that code.
func TestMigrateFailsFastWhenSyncHoldsLock(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	name := "migrateidx"
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: name, DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatalf("initial build: %v", err)
	}
	base := filepath.Join(env.cfg.IndexDir, name, name)
	formatBefore := gleann.StoreFormatOf(base)
	lockPath := filepath.Join(env.cfg.IndexDir, name+".lock")

	// The child holds the very lock Sync takes for its whole operation.
	h := startSyncLockHolder(t, env.cfg.IndexDir, name)

	start := time.Now()
	err := gleann.MigrateIndex(env.cfg.IndexDir, name, gleann.MigrateOptions{})
	if err == nil {
		t.Fatal("MigrateIndex succeeded while a sync holds the index lock")
	}
	if !strings.Contains(err.Error(), "held by another process") || !strings.Contains(err.Error(), name) {
		t.Fatalf("MigrateIndex lock error = %v; want a readable lock message naming the index", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("MigrateIndex waited %s; it must fail fast", elapsed)
	}
	if gleann.StoreFormatOf(base) != formatBefore {
		t.Error("a locked-out migration changed the store format")
	}
	// Rollback and cleanup take the same lock and must fail fast too.
	if err := gleann.RollbackMigration(env.cfg.IndexDir, name); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Errorf("RollbackMigration with the lock held = %v; want the lock message", err)
	}
	if err := gleann.CleanupMigration(env.cfg.IndexDir, name); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Errorf("CleanupMigration with the lock held = %v; want the lock message", err)
	}

	// The OS freed the lock when the holder died.
	h.stop()
	l, err := store.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("lock still held after holder died: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
