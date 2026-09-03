//go:build js && wasm

// Package osutil pkg/util/osutil/privileges_js.go c0-com-util
//
// js/wasm: no setuid in a browser sandbox — mirror the windows no-op shape.
package osutil

// GainRoot is a no-op on js/wasm; there are no OS privileges to gain.
func GainRoot() (int, error) {
	return 0, nil
}

// ReleaseRoot is a no-op on js/wasm.
func ReleaseRoot(_ int) error {
	return nil
}
