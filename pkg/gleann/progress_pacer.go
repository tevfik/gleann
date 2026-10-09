package gleann

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// ProgressSurface defines the output format for progress updates.
type ProgressSurface string

const (
	SurfaceAuto  ProgressSurface = "auto"
	SurfacePlain ProgressSurface = "plain"
	SurfaceJSON  ProgressSurface = "json"
	SurfaceNone  ProgressSurface = "none"
)

// Pacer coordinates progress reporting with transcript protection for AI agents.
type Pacer struct {
	surface        ProgressSurface
	out            io.Writer
	minBarInterval time.Duration
	lastBarTime    time.Time
	lastPhase      string
	mu             sync.Mutex
	startTime      time.Time
}

// NewPacer creates a new Pacer.
func NewPacer(surface ProgressSurface, out io.Writer) *Pacer {
	if out == nil {
		out = os.Stderr
	}
	if surface == "" || surface == SurfaceAuto {
		surface = SurfacePlain
	}
	return &Pacer{
		surface:        surface,
		out:            out,
		minBarInterval: 10 * time.Second,
		startTime:      time.Now(),
	}
}

// Tick emits a paced progress update if minimum interval elapsed or on phase transition.
func (p *Pacer) Tick(phase string, itemsDone, totalItems int, bytesDone, totalBytes int64, force bool) {
	if p.surface == SurfaceNone {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	phaseChanged := p.lastPhase != phase
	intervalPassed := now.Sub(p.lastBarTime) >= p.minBarInterval

	if !force && !phaseChanged && !intervalPassed {
		return
	}

	p.lastBarTime = now
	p.lastPhase = phase

	// Compute percentages
	pct := 0.0
	if totalBytes > 0 {
		pct = (float64(bytesDone) / float64(totalBytes)) * 100.0
	} else if totalItems > 0 {
		pct = (float64(itemsDone) / float64(totalItems)) * 100.0
	}

	// Compute rate and ETA
	elapsedSec := now.Sub(p.startTime).Seconds()
	rate := 0.0
	etaSec := 0.0
	if elapsedSec > 0.1 && bytesDone > 0 {
		rate = float64(bytesDone) / elapsedSec
		if totalBytes > bytesDone && rate > 0 {
			etaSec = float64(totalBytes-bytesDone) / rate
		}
	}

	barWidth := 20
	filled := int((pct / 100.0) * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	barStr := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)

	if p.surface == SurfacePlain {
		// Line 1: Human-readable bar
		fmt.Fprintf(p.out, "gleann-bar [%s] %5.1f%% | %s | %d/%d items | %.1fMB/s | eta %.0fs\n",
			barStr, pct, phase, itemsDone, totalItems, rate/(1024*1024), etaSec)

		// Line 2: Machine-readable key=value
		fmt.Fprintf(p.out, "gleann-progress phase=%s pct=%.1f items=%d/%d bytes=%d/%d rate=%.1f eta_s=%.1f\n",
			phase, pct, itemsDone, totalItems, bytesDone, totalBytes, rate, etaSec)
	} else if p.surface == SurfaceJSON {
		fmt.Fprintf(p.out, `{"phase":"%s","pct":%.1f,"items_done":%d,"total_items":%d,"bytes_done":%d,"total_bytes":%d,"rate_bytes_sec":%.1f,"eta_sec":%.1f}`+"\n",
			phase, pct, itemsDone, totalItems, bytesDone, totalBytes, rate, etaSec)
	}
}

// Done emits the final terminal summary line.
func (p *Pacer) Done(ok bool, exitCode int, files int, totalBytes int64) {
	if p.surface == SurfaceNone {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	wallSec := time.Since(p.startTime).Seconds()

	if p.surface == SurfacePlain {
		fmt.Fprintf(p.out, "gleann-done ok=%t exit=%d files=%d bytes=%d wall=%.2fs\n",
			ok, exitCode, files, totalBytes, wallSec)
	} else if p.surface == SurfaceJSON {
		fmt.Fprintf(p.out, `{"done":true,"ok":%t,"exit":%d,"files":%d,"bytes":%d,"wall_sec":%.2f}`+"\n",
			ok, exitCode, files, totalBytes, wallSec)
	}
}
