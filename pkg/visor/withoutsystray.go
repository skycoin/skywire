//go:build withoutsystray
// +build withoutsystray

// Package visor pkg/visor/withoutsystray.go c3-vis-core
package visor

import (
	"sync"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	stopVisorFnMx sync.Mutex //nolint:unused
	stopVisorFn   func()
)

// RunSystray is a no-op: this build has no systray.
func RunSystray(_ *visorconfig.V1, _ Options) {}

// RunTrayOnly exits: this build has no systray.
func RunTrayOnly(_ string) {
	// systray not available in this build (built with the 'withoutsystray' tag).
	mLog.Fatal("this build has no systray support (built with the 'withoutsystray' tag); rebuild without it to use --systray-only")
}
