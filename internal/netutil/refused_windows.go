//go:build windows

package netutil

import "syscall"

// wsaeconnrefused is WSAECONNREFUSED, the errno Winsock returns for a refused
// connection; the syscall package does not export it. syscall.ECONNREFUSED is
// a value Go invented for Windows, which a dial there never returns.
const wsaeconnrefused syscall.Errno = 10061

var refusedErrnos = []error{wsaeconnrefused, syscall.ECONNREFUSED}
