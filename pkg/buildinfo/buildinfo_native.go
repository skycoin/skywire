//go:build !js

// Package buildinfo pkg/buildinfo/buildinfo_native.go
//
// Native init() that parses the ldflags-injected `go list -m -json`
// output via encoding/json. Build-tag-gated off the WASM path —
// see buildinfo.go's package doc for the rationale.
package buildinfo

import (
	"encoding/json"
)

func init() {
	// Use ldflags-provided `golist` info if available — produces the
	// canonical version + commit info on release builds.
	if golist != "" {
		var mInfo ModuleInfo
		if err := json.Unmarshal([]byte(golist), &mInfo); err == nil {
			if mInfo.Version != "" && version == unknown {
				version = mInfo.Version
			}
			if mInfo.Origin.Hash != "" && commit == unknown {
				commit = mInfo.Origin.Hash
			}
		}
	}
	// Always read build info — needed for DepVersion even when
	// version is provided via ldflags.
	readDebugBuildInfo()
}
