//go:build !js

// Package visor pkg/visor/netonline_native.go c3-vis-core
package visor

import "context"

// watchBrowserOnline is the browser's online and offline events, which a
// native visor does not have.
func watchBrowserOnline(context.Context, *Visor) {}
