package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// buildHealthy builds a small format-2 index and returns its base path.
func buildHealthy(t *testing.T, dir, name string) string {
	t.Helper()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), name, f2Items()); err != nil {
		t.Fatal(err)
	}
	if h, err := CheckIndexHealth(dir, name); err != nil || h.NeedsRepair() {
		t.Fatalf("fresh index unhealthy: %+v, %v", h, err)
	}
	return filepath.Join(dir, name, name)
}

func problemsContain(h *IndexHealth, s string) bool {
	return strings.Contains(strings.Join(h.Problems, "\n"), s)
}

func TestIndexHealthReportsDamage(t *testing.T) {
	dir := t.TempDir()
	if _, err := CheckIndexHealth(dir, "../x"); err == nil {
		t.Fatal("a path name was accepted")
	}
	if _, err := CheckIndexHealth(dir, "absent"); err == nil || !strings.Contains(err.Error(), "read metadata") {
		t.Fatalf("missing index: %v", err)
	}

	t.Run("store meta records another format", func(t *testing.T) {
		base := buildHealthy(t, dir, "fmt")
		st, err := store.Open(base+".db", store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetMeta(context.Background(), "store_format", "7"); err != nil {
			t.Fatal(err)
		}
		st.Close()
		h, err := CheckIndexHealth(dir, "fmt")
		if err != nil {
			t.Fatal(err)
		}
		// store.Open refuses the unknown format, so the store is unreadable.
		if !h.NeedsRepair() || h.Repairable || !problemsContain(h, "cannot be read") {
			t.Fatalf("health = %+v", h)
		}
	})

	t.Run("delta of other dimensions", func(t *testing.T) {
		base := buildHealthy(t, dir, "dims")
		d := newVectorDelta(3, deltaDTypeF32, "l2")
		_ = d.Append(0, []float32{1, 2, 3})
		if err := os.WriteFile(base+suffixDelta, d.Marshal(), 0o644); err != nil {
			t.Fatal(err)
		}
		h, _ := CheckIndexHealth(dir, "dims")
		if !problemsContain(h, "3-dimensional vectors, the index 8") || !h.Repairable {
			t.Fatalf("health = %+v", h)
		}
	})

	t.Run("unreadable delta", func(t *testing.T) {
		base := buildHealthy(t, dir, "baddelta")
		if err := os.WriteFile(base+suffixDelta, []byte("garbage"), 0o644); err != nil {
			t.Fatal(err)
		}
		h, _ := CheckIndexHealth(dir, "baddelta")
		if !problemsContain(h, "delta vector file cannot be read") {
			t.Fatalf("health = %+v", h)
		}
	})

	t.Run("damaged database", func(t *testing.T) {
		base := buildHealthy(t, dir, "corrupt")
		if err := removeDBFiles(base + ".db"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".db", []byte(strings.Repeat("not sqlite ", 600)), 0o644); err != nil {
			t.Fatal(err)
		}
		h, _ := CheckIndexHealth(dir, "corrupt")
		if h.Repairable || !problemsContain(h, "cannot be read") {
			t.Fatalf("health = %+v", h)
		}
	})
}
