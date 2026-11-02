//go:build !windows

// Package netutil pkg/netutil/accept_errno_other.go c0-com-util
package netutil

import "syscall"

var transientAcceptErrnos = []error{
	syscall.ECONNRESET, syscall.ECONNABORTED,
	syscall.EINTR, syscall.EMFILE, syscall.ENFILE,
}
