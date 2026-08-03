//go:build withoutsystray
// +build withoutsystray

// Package visor pkg/visor/withoutsystray.go c3-vis-core
package visor

import (
	"context"
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
	err := run(context.Background(), nil)
	if err != nil {
		mLog.WithError(err).Fatal("a fatal error occurred")
	}
}

func runTrayOnly() {
	// systray not available in this build (built with the 'withoutsystray' tag).
	mLog.Fatal("this build has no systray support (built with the 'withoutsystray' tag); rebuild without it to use --systray-only")
}
