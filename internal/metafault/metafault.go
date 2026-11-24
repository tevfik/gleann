// Package metafault is a fault-injection seam for the index metadata write
// (gleann.UpdateIndexMeta and the full build's replacement of the file).
// Production code never sets it; tests use Set to make the write fail after the index store is open, which no file-system trick can
// do portably (it would also fail the store open, and does not work as root or
// on Windows).
package metafault

import "sync/atomic"

var hook atomic.Pointer[func(metaPath string) error]

// Check returns the injected error for a write of metaPath, or nil.
func Check(metaPath string) error {
	if fn := hook.Load(); fn != nil {
		return (*fn)(metaPath)
	}
	return nil
}

// Set installs fn (nil clears it) and returns the function that restores the
// previous hook. Tests call it as t.Cleanup(metafault.Set(fn)).
func Set(fn func(metaPath string) error) (restore func()) {
	prev := hook.Load()
	if fn == nil {
		hook.Store(nil)
	} else {
		hook.Store(&fn)
	}
	return func() { hook.Store(prev) }
}
