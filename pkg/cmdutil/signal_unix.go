//go:build !windows
// +build !windows

// Package cmdutil pkg/cmdutil/signal_unix.go
package cmdutil

import (
	"os"

	"golang.org/x/sys/unix"
)

func listenSignals() []os.Signal {
	return []os.Signal{unix.SIGINT, unix.SIGTERM, unix.SIGQUIT}
}
