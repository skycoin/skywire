//go:build mobile && !js

// Package execwasm pkg/wasmhv/execwasm/embed_mobile.go c3-wasm-embed
package execwasm

// The mobile build serves no desk — Hypervisor.execModule is hardcoded false
// there — so the 38 MB command module it would be hosted out of is dead weight
// on a phone, and embedding it put libskywire-mobile.so 31 MB over its size
// budget. The phone core is API-only and carries no copy of the module.
func embeddedGz() []byte { return nil }

// No blob, so no recorded revision either.
func embeddedRevision() string { return "" }
