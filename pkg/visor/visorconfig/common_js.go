//go:build tinygo

// Package visorconfig pkg/visor/visorconfig/common_js.go c3-vis-core
//
// TinyGo stub for Common.flush. The real implementation lives in
// common_native.go (encoding/json + os.WriteFile, neither
// reachable under TinyGo). Stub returns an error rather than
// panicking so any unexpected call site under js fails loudly
// instead of taking down the whole WASM runtime — but no live
// call path in the install-page WASM ever reaches it; the stub
// exists to satisfy the type-checker for v1.go's mutator methods
// (which TinyGo dead-code-eliminates since the WASM main never
// invokes them).
package visorconfig

import (
	"errors"
)

// errFlushUnderJS is the sentinel returned by flush in the js build.
var errFlushUnderJS = errors.New("visorconfig: Common.flush unavailable under tinygo")

func (c *Common) flush(_ interface{}) error {
	return errFlushUnderJS
}
