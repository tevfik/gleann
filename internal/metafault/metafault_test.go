package metafault

import (
	"errors"
	"sync"
	"testing"
)

func TestCheckWithoutHook(t *testing.T) {
	if err := Check("x"); err != nil {
		t.Fatalf("Check with no hook = %v, want nil", err)
	}
}

func TestSetRestoreNesting(t *testing.T) {
	errA, errB := errors.New("a"), errors.New("b")
	restoreA := Set(func(string) error { return errA })
	if err := Check("p"); err != errA {
		t.Fatalf("with hook A: %v", err)
	}
	restoreB := Set(func(string) error { return errB })
	if err := Check("p"); err != errB {
		t.Fatalf("with hook B: %v", err)
	}
	restoreB()
	if err := Check("p"); err != errA {
		t.Fatalf("after restoring B: %v, want A back", err)
	}
	clear := Set(nil)
	if err := Check("p"); err != nil {
		t.Fatalf("after Set(nil): %v", err)
	}
	clear()
	if err := Check("p"); err != errA {
		t.Fatalf("after undoing the clear: %v, want A", err)
	}
	restoreA()
	if err := Check("p"); err != nil {
		t.Fatalf("after restoring A: %v, want nil", err)
	}
}

func TestHookReceivesThePath(t *testing.T) {
	var got string
	t.Cleanup(Set(func(p string) error { got = p; return nil }))
	_ = Check("/some/idx.meta.json")
	if got != "/some/idx.meta.json" {
		t.Fatalf("hook saw %q", got)
	}
}

// Check and Set race freely; -race must stay quiet.
func TestConcurrentUse(t *testing.T) {
	t.Cleanup(Set(nil))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = Check("p")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				restore := Set(func(string) error { return nil })
				restore()
			}
		}()
	}
	wg.Wait()
}
