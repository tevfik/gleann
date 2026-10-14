package indexing

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Progress is a machine-readable progress update emitted by Build.
//
// Pct, Rate (bytes/sec) and ETASec are derived from BytesDone/TotalBytes
// using the same formulas as gleann.Pacer so callers that do not render a
// pacer (e.g. the MCP server) can report them directly.
type Progress struct {
	Phase      string
	ItemsDone  int
	TotalItems int
	BytesDone  int64
	TotalBytes int64
	// Force is true on phase boundaries and completion; renderers that
	// rate-limit output (gleann.Pacer) should always emit forced updates.
	Force  bool
	Pct    float64
	Rate   float64
	ETASec float64
}

// ProgressFunc receives progress updates. It may be called concurrently
// from embedding workers and must be cheap and goroutine-safe.
type ProgressFunc func(Progress)

// Options are shared by Build and Sync.
type Options struct {
	// Config is the effective gleann configuration (index dir, backend,
	// embedding provider/model, chunking, ...). Required.
	Config gleann.Config

	// Mode filters which files are considered (default ModeAll).
	Mode Mode
	// NoPlugins disables external document-extraction plugins. ModeCode
	// always disables plugins.
	NoPlugins bool
	// IncludeSubmodules walks into Git submodule directories.
	IncludeSubmodules bool
	// MultimodalModel overrides Config.MultimodalModel for media files.
	MultimodalModel string

	// Embedder overrides the embedding computer built from Config. When nil
	// an embedding.Computer is created (and, for the "llamacpp" provider, an
	// embedded llama-server is started for the duration of the call). The
	// embedder is always wrapped in an embedding cache.
	Embedder gleann.EmbeddingComputer

	// Tracker overrides the vault file tracker. When nil the default
	// tracker (~/.gleann/vault/vault.db) is shared process-wide; use
	// NoTracker to disable tracking entirely.
	Tracker   *vault.Tracker
	NoTracker bool

	// Stdout / Stderr receive human-readable status and warning messages
	// (the same lines the CLI prints). Nil writers discard output.
	Stdout io.Writer
	Stderr io.Writer

	// Progress receives machine-readable progress updates (may be nil).
	Progress ProgressFunc
}

func (o *Options) mode() Mode {
	if o.Mode == "" {
		return ModeAll
	}
	return o.Mode
}

func (o *Options) multimodalModel() string {
	if o.MultimodalModel != "" {
		return o.MultimodalModel
	}
	return o.Config.MultimodalModel
}

func (o *Options) printer() *printer {
	return newPrinter(o.Stdout, o.Stderr)
}

// printer serialises writes to the caller-supplied writers; the read phase
// logs from several worker goroutines and callers may pass a single
// non-thread-safe buffer for both streams.
type printer struct {
	mu  sync.Mutex
	out io.Writer
	err io.Writer
}

func newPrinter(out, errw io.Writer) *printer {
	if out == nil {
		out = io.Discard
	}
	if errw == nil {
		errw = io.Discard
	}
	return &printer{out: out, err: errw}
}

func (p *printer) Printf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.out, format, a...)
}

func (p *printer) Warnf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.err, format, a...)
}

// progressEmitter derives Pct/Rate/ETA and forwards to a ProgressFunc.
type progressEmitter struct {
	fn    ProgressFunc
	start time.Time
}

func newProgressEmitter(fn ProgressFunc) *progressEmitter {
	return &progressEmitter{fn: fn, start: time.Now()}
}

func (e *progressEmitter) emit(phase string, done, total int, bytesDone, totalBytes int64, force bool) {
	if e == nil || e.fn == nil {
		return
	}
	p := Progress{
		Phase: phase, ItemsDone: done, TotalItems: total,
		BytesDone: bytesDone, TotalBytes: totalBytes, Force: force,
	}
	if totalBytes > 0 {
		p.Pct = float64(bytesDone) / float64(totalBytes) * 100.0
	} else if total > 0 {
		p.Pct = float64(done) / float64(total) * 100.0
	}
	if elapsed := time.Since(e.start).Seconds(); elapsed > 0.1 && bytesDone > 0 {
		p.Rate = float64(bytesDone) / elapsed
		if totalBytes > bytesDone && p.Rate > 0 {
			p.ETASec = float64(totalBytes-bytesDone) / p.Rate
		}
	}
	e.fn(p)
}
