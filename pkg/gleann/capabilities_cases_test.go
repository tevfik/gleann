//go:build !eif && !native && !(cgo && faiss)

package gleann

import "testing"

// Without the optional build tags the pure-Go stubs must report every
// accelerated backend as unsupported (AGENTS.md: graceful degradation).
func TestCapabilityStubsReportUnsupported(t *testing.T) {
	if IsEIFSupported() || IsNativeSupported() || IsFaissSupported() {
		t.Errorf("stubs: eif=%v native=%v faiss=%v, want all false", IsEIFSupported(), IsNativeSupported(), IsFaissSupported())
	}
}
