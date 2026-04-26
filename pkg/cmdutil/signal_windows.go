//go:build windows
// +build windows

// Package cmdutil pkg/cmdutil/signal_windows.go
package cmdutil

import (
	"os"

	"golang.org/x/sys/windows"
)

func listenSignals() []os.Signal {
	return []os.Signal{os.Interrupt, windows.SIGINT, windows.SIGTERM, windows.SIGQUIT}
}
