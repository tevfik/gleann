package graphdb

import (
	"os"
	"strconv"
	"strings"
)

// Memory is measured from /proc/self/status, so it is reported on Linux only
// (0 elsewhere). The peak (VmHWM) is reset per backend by writing 5 to
// /proc/self/clear_refs, so each backend's peak is its own; the harness's
// fixture is part of every backend's baseline and cancels in the delta.

func statusKB(key string) int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, key+":") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(line, key+":"))
		if len(f) == 0 {
			return 0
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		return n
	}
	return 0
}

func currentRSSKB() int64 { return statusKB("VmRSS") }
func peakRSSKB() int64    { return statusKB("VmHWM") }

func resetPeakRSS() {
	_ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0)
}
