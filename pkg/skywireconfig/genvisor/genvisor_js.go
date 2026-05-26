//go:build js

// Package genvisor pkg/skywireconfig/genvisor/genvisor_js.go
//
// js/wasm implementation of MustMarshalJSON. Cannot use
// encoding/json — its reflect-based marshal calls reflect.unsafe_New
// / mapassign / typedmemmove which TinyGo's stdlib doesn't provide.
// This stub returns a sentinel JSON error so the install page's
// "Download skywire.json" button reports clearly that the feature
// is unavailable in the small (TinyGo) WASM build.
//
// FOLLOWUP: a hand-rolled streaming serializer that walks V1's
// exported fields without reflection would restore full
// functionality under TinyGo. V1 has ~40 fields including nested
// structs (Dmsg, Transport, Routing, Launcher, ...), so the
// implementation is non-trivial — tracking as a TODO until the
// install page's small-blob variant is in active use.
package genvisor

import "github.com/skycoin/skywire/pkg/visor/visorconfig"

// MustMarshalJSON returns a sentinel error JSON under js/wasm.
// Non-WASM (genvisor_native.go) uses encoding/json.MarshalIndent.
func MustMarshalJSON(_ *visorconfig.V1) []byte {
	return []byte(`{"error":"skywire.json generation unavailable in TinyGo WASM build; use the standard /generator/ install page (Go WASM)"}`)
}
