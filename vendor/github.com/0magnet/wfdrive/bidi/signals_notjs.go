//go:build !js

package bidi

import (
	"os"
	"syscall"
)

// shutdownSignals are the signals a driver must catch to release the BiDi
// session before the process goes away. Firefox does not reclaim the session
// when the socket merely drops, so an uncaught signal costs a browser restart.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
