//go:build !js

// Package visor pkg/visor/hostbridge_probe_other.go c3-vis-wasm
package visor

import "context"

// probeLocalAccess has nothing to ask outside a browser.
func probeLocalAccess(context.Context, string) {}
