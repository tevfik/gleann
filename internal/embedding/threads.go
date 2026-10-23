//go:build eif

package embedding

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	threadsMu   sync.Mutex
	threadsAt   time.Time
	threadsLast int
)

// embedThreads picks the OpenMP worker count for in-process embedding.
//
// Left alone, libgomp starts one thread per logical CPU. When another job (a
// training run, a build) already keeps part of the machine busy, those threads
// fight over the same cores and embedding gets several times slower than with
// a smaller team. So unless OMP_NUM_THREADS is set, use the cores that are
// currently idle: logical CPUs minus the 1-minute load average, at least 4.
// The load is sampled at most every few seconds, so the team size follows the
// machine without reading /proc for every chunk.
func embedThreads() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("OMP_NUM_THREADS"))); err == nil && v > 0 {
		return v
	}
	now := time.Now()
	threadsMu.Lock()
	defer threadsMu.Unlock()
	if threadsAt.IsZero() || now.Sub(threadsAt) > 5*time.Second {
		threadsLast = threadsFor(runtime.NumCPU(), readLoad1())
		threadsAt = now
	}
	return threadsLast
}

// threadsFor returns the team size for ncpu logical CPUs and a 1-minute load
// average (negative when unknown).
func threadsFor(ncpu int, load1 float64) int {
	if ncpu < 1 {
		ncpu = 1
	}
	if load1 < 0 {
		return ncpu
	}
	n := ncpu - int(load1+0.5)
	if n < 4 {
		n = 4
	}
	if n > ncpu {
		n = ncpu
	}
	return n
}

// readLoad1 returns the 1-minute load average, or -1 when it is not available.
func readLoad1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return -1
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return -1
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return -1
	}
	return v
}
