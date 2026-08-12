//go:build !js

// Package wasmhv export_test.go: test-only exported aliases for the internal
// wasm RPC argument mirror types, so the gob-mirror test can live in the
// EXTERNAL test package (wasmhv_test) and import pkg/visor without forming an
// import cycle. pkg/visor now imports pkg/wasmhv (ServeWasm serves the embedded
// wasm assets), so an internal (package wasmhv) test that imports pkg/visor
// would be a cycle "wasmhv[test] → visor → wasmhv". Keeping the assertions in
// wasmhv_test avoids that; these aliases just let it construct the argument
// mirrors whose fields are already exported.
package wasmhv

// Mirror-prefixed so they don't collide with the exported TransportsIn /
// AddTransportIn already declared in rpc_gateway.go (distinct types).
type (
	// MirrorStartAppIn aliases the internal startAppIn wasm RPC arg mirror.
	MirrorStartAppIn = startAppIn
	// MirrorSetAutoStartIn aliases the internal setAutoStartIn wasm RPC arg mirror.
	MirrorSetAutoStartIn = setAutoStartIn
	// MirrorTransportsIn aliases the internal transportsIn wasm RPC arg mirror.
	MirrorTransportsIn = transportsIn
	// MirrorAddTransportIn aliases the internal addTransportIn wasm RPC arg mirror.
	MirrorAddTransportIn = addTransportIn
)
