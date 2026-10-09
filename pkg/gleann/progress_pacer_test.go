package gleann

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPacer_PacedOutput(t *testing.T) {
	var buf bytes.Buffer
	pacer := NewPacer(SurfacePlain, &buf)
	pacer.minBarInterval = 5 * time.Second

	// Tick 1 (initial, forced)
	pacer.Tick("walk", 10, 100, 1024, 10240, true)
	out1 := buf.String()
	if !strings.Contains(out1, "gleann-bar") || !strings.Contains(out1, "gleann-progress") {
		t.Fatalf("expected initial bar and progress, got: %s", out1)
	}

	buf.Reset()

	// Tick 2 immediately after (not forced, same phase) -> should NOT emit bar due to pacing
	pacer.Tick("walk", 15, 100, 1500, 10240, false)
	out2 := buf.String()
	if strings.Contains(out2, "gleann-bar") {
		t.Errorf("consecutive tick without interval should not emit gleann-bar, got: %s", out2)
	}

	buf.Reset()

	// Tick 3: Phase change (from walk to index) -> should emit bar immediately!
	pacer.Tick("index", 20, 100, 2000, 10240, false)
	out3 := buf.String()
	if !strings.Contains(out3, "gleann-bar") || !strings.Contains(out3, "phase=index") {
		t.Errorf("phase change must emit bar immediately, got: %s", out3)
	}

	buf.Reset()

	// Done line
	pacer.Done(true, 0, 100, 10240)
	doneOut := buf.String()
	if !strings.Contains(doneOut, "gleann-done ok=true exit=0") {
		t.Errorf("expected gleann-done line, got: %s", doneOut)
	}
}

func TestPacer_SurfaceNone(t *testing.T) {
	var buf bytes.Buffer
	pacer := NewPacer(SurfaceNone, &buf)

	pacer.Tick("walk", 10, 100, 100, 1000, true)
	pacer.Done(true, 0, 100, 1000)

	if buf.Len() > 0 {
		t.Errorf("SurfaceNone should emit zero bytes, got: %s", buf.String())
	}
}
