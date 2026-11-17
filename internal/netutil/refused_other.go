//go:build !windows

package netutil

import "syscall"

var refusedErrnos = []error{syscall.ECONNREFUSED}
