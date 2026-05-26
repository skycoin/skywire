//go:build js

// Package buildinfo pkg/buildinfo/buildinfo_js.go
//
// js/wasm init() — skips the ldflags-injected `golist` parse path
// (which would need encoding/json). The install-page WASM doesn't
// use ldflags-injection; its version info comes purely from
// debug.ReadBuildInfo via readDebugBuildInfo. See buildinfo.go's
// package doc for the broader rationale on keeping encoding/json
// out of the WASM build graph.
package buildinfo

func init() {
	readDebugBuildInfo()
}
