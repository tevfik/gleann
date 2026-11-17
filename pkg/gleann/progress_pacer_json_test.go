package gleann

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPacerJSONSurfaceRateAndETA(t *testing.T) {
	var buf bytes.Buffer
	p := NewPacer(SurfaceJSON, &buf)
	p.startTime = time.Now().Add(-10 * time.Second) // 10 s elapsed: the rate is measurable

	p.Tick("embed", 0, 0, 4<<20, 8<<20, false)
	p.Tick("embed", 1, 2, 5<<20, 8<<20, false) // same phase, within the interval: paced out
	p.Tick("write", 3, 4, 0, 0, false)         // items only
	p.Tick("write", 9, 4, 0, 0, true)          // over 100%: the bar is clamped
	p.Done(true, 0, 4, 8<<20)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4 (one tick paced out):\n%s", len(lines), buf.String())
	}
	var first struct {
		Phase string  `json:"phase"`
		Pct   float64 `json:"pct"`
		Rate  float64 `json:"rate_bytes_sec"`
		ETA   float64 `json:"eta_sec"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("not JSON: %q (%v)", lines[0], err)
	}
	if first.Phase != "embed" || first.Pct != 50 || first.Rate <= 0 || first.ETA < 9 || first.ETA > 11 {
		t.Fatalf("first tick = %+v, want 50%%, a rate and an ETA of about 10 s", first)
	}
	var second struct {
		Pct float64 `json:"pct"`
	}
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if second.Pct != 75 {
		t.Fatalf("items-only progress = %v%%, want 75", second.Pct)
	}
	var done struct {
		Done  bool `json:"done"`
		Files int  `json:"files"`
	}
	if err := json.Unmarshal([]byte(lines[3]), &done); err != nil || !done.Done || done.Files != 4 {
		t.Fatalf("done line = %q", lines[3])
	}

	// The plain surface clamps an over-full bar to its width.
	var plain bytes.Buffer
	pp := NewPacer("", &plain)
	pp.Tick("x", 9, 4, 0, 0, true)
	if !strings.Contains(plain.String(), "["+strings.Repeat("#", 20)+"]") {
		t.Fatalf("plain bar = %q", plain.String())
	}
	if NewPacer(SurfacePlain, nil).out == nil {
		t.Fatal("a nil writer must default to stderr")
	}
}
