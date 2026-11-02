//go:build windows

// Package netutil pkg/netutil/accept_errno_windows.go c0-com-util
package netutil

import "syscall"

var transientAcceptErrnos = []error{
	syscall.WSAECONNRESET, syscall.WSAECONNABORTED,
	syscall.EINTR, syscall.EMFILE,
}
