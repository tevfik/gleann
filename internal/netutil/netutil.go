// Package netutil classifies network errors the same way on every platform.
package netutil

import (
	"errors"
	"strings"
)

// IsConnectionRefused reports whether err means nothing is listening at the
// address dialled (the server is not running). It checks the errno through
// the error chain (*url.Error, *net.OpError, *os.SyscallError), so it works
// whatever the platform calls the error: Unix reports ECONNREFUSED
// ("connection refused"), Windows WSAECONNREFUSED ("No connection could be
// made because the target machine actively refused it"). Errors that lost
// their chain (formatted with %v) are matched by either message.
func IsConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	for _, errno := range refusedErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "actively refused")
}
