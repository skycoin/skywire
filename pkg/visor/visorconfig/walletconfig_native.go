//go:build !wasm && !(js && wasm)

// Package visorconfig pkg/visor/visorconfig/walletconfig_native.go c3-app-wallet
package visorconfig

// walletCustodyDiskCapable: this build has a real filesystem and can run the
// skycoin-web server, so disk custody is realizable. Covers the native visor
// and `hv serve` (which is the same binary).
const walletCustodyDiskCapable = true
