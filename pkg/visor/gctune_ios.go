//go:build ios

// Package visor pkg/visor/gctune_ios.go c3-vis-core
package visor

import (
	"os"
	"runtime/debug"

	"github.com/skycoin/skywire/pkg/logging"
)

// iosDefaultGCPercent sizes the heap at 1.25x the live set. iOS ends a process past its
// memory limit, so memory is the scarcer resource (ios/gates/evidence/G8/README.md).
const iosDefaultGCPercent = 25

// applyGCTuning sets the iOS GOGC default, unless GOGC is in the environment (as gctune_js.go).
func applyGCTuning(log *logging.Logger) {
	if os.Getenv("GOGC") != "" {
		return
	}
	prev := debug.SetGCPercent(iosDefaultGCPercent)
	log.Infof("GOGC set to %d on iOS (was %d)", iosDefaultGCPercent, prev)
}
