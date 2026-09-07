//go:build js

package bidi

import (
	"os"
	"syscall"
)

// shutdownSignals omits SIGHUP: js/wasm does not define it, and this package is
// linked into binaries that compile for GOOS=js. Naming it unconditionally
// broke that build, which is worth a two-file split — the alternative is that
// embedding a browser driver quietly costs you the wasm target.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
