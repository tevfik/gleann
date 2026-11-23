//go:build windows

package fsretry

import (
	"os"
	"testing"
)

func TestIsTransientWindowsErrors(t *testing.T) {
	for _, e := range []error{errorAccessDenied, errorSharingViolation} {
		if !isTransient(&os.LinkError{Op: "rename", Err: e}) {
			t.Errorf("%v must be transient", e)
		}
	}
	if isTransient(os.ErrNotExist) {
		t.Error("not-exist must not be transient")
	}
}
