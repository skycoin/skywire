//go:build !windows && !(js && wasm)

// Package cmdutil pkg/cmdutil/signal_unix.go c1-com-util
package cmdutil

import (
	"os"

	"golang.org/x/sys/unix"
)

func listenSignals() []os.Signal {
	return []os.Signal{unix.SIGINT, unix.SIGTERM, unix.SIGQUIT}
}
