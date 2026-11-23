package fsretry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var errBusy = errors.New("sharing violation")

func busy(err error) bool { return errors.Is(err, errBusy) }

func TestRenameRetriesTransientErrorThenSucceeds(t *testing.T) {
	calls, slept := 0, time.Duration(0)
	fn := func(o, n string) error {
		calls++
		if calls <= 3 {
			return &os.LinkError{Op: "rename", Old: o, New: n, Err: errBusy}
		}
		return nil
	}
	if err := rename(fn, busy, func(d time.Duration) { slept += d }, time.Second, "a", "b"); err != nil {
		t.Fatalf("err = %v, want success after the transient failures", err)
	}
	if calls != 4 {
		t.Errorf("rename called %d times, want 4", calls)
	}
	if slept == 0 || slept > time.Second {
		t.Errorf("slept %v, want within (0, 1s]", slept)
	}
}

func TestRenameGivesUpAfterTheBudget(t *testing.T) {
	calls, slept := 0, time.Duration(0)
	fn := func(string, string) error { calls++; return errBusy }
	err := rename(fn, busy, func(d time.Duration) { slept += d }, time.Second, "a", "b")
	if !errors.Is(err, errBusy) {
		t.Fatalf("err = %v, want the last transient error", err)
	}
	if slept != time.Second {
		t.Errorf("slept %v, want exactly the 1s budget", slept)
	}
	if calls < 5 {
		t.Errorf("only %d attempts within the budget", calls)
	}
}

func TestRenameDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	other := errors.New("no such file")
	fn := func(string, string) error { calls++; return other }
	if err := rename(fn, busy, func(time.Duration) { t.Error("slept on a permanent error") }, time.Second, "a", "b"); !errors.Is(err, other) || calls != 1 {
		t.Errorf("err = %v calls = %d, want the error after one call", err, calls)
	}
}

func TestRenameReplacesAFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Errorf("dst = %q", b)
	}
}
