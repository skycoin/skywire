//go:build wasm || (js && wasm)

// Package visorconfig pkg/visor/visorconfig/walletconfig_wasm.go c3-app-wallet
package visorconfig

// walletCustodyDiskCapable: a browser visor runs the skycoin-web app in-process
// with its wallet files in the tab's persistent filesystem (/opt/skywire).
const walletCustodyDiskCapable = true
