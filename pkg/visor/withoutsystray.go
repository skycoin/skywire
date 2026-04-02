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
	// no-op: systray not available
}
