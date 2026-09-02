//go:build !js

// Package cmdutil pkg/cmdutil/signal_platform_native.go c0-com-util
package cmdutil

import "os"

// notifyPlatformInterrupt is a no-op on native platforms — signal.Notify
// already receives real POSIX signals. The js twin registers a JS-callable
// interrupt (see signal_js.go).
func notifyPlatformInterrupt(chan<- os.Signal) {}
