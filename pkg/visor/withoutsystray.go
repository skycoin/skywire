//go:build withoutsystray
// +build withoutsystray

// Package visor pkg/visor/withoutsystray.go
package visor

import (
	"sync"
)

var (
	stopVisorFnMx sync.Mutex //nolint:unused
	stopVisorFn   func()
)

func runAppSystray() {
	// no-op: systray not available
}

func runApp() {
	err := run(nil)
	if err != nil {
		mLog.WithError(err).Fatal("a fatal error occurred")
	}
}
