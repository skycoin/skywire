//go:build wasm || (js && wasm)

// Package visorconfig pkg/visor/visorconfig/walletconfig_wasm.go c3-app-wallet
package visorconfig

// walletCustodyDiskCapable: a browser build has no backend process and no
// filesystem to hold wallet files, so disk custody cannot be realized here.
// A config carrying custody:"disk" (written by a native visor) clamps to
// browser rather than offering a mode that would fail at use time.
const walletCustodyDiskCapable = false
