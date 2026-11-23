//go:build windows

package fsretry

import (
	"errors"
	"syscall"
)

const (
	errorAccessDenied     syscall.Errno = 5  // ERROR_ACCESS_DENIED
	errorSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
)

// isTransient reports whether err is the sharing error Windows returns while
// another handle keeps the rename target open.
func isTransient(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == errorAccessDenied || errno == errorSharingViolation)
}
