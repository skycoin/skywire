//go:build mobile && !tinygo && !js

// Package wasmbin pkg/wasmhv/wasmbin/select_mobile.go c3-vis-wasm
package wasmbin

// A mobile build embeds NEITHER wasm-visor pair.
//
// The blobs exist so a visor SERVING the browser PWA can hand a browser a
// wasm-visor with no external --wasm file. An Android app is not that server:
// it is the phone's own visor, and nothing in the mobile build reaches
// GetVariant. Embedding them cost the android library 16.4 MB — 12.3 MB for
// wasmgo..gobytes plus 4.1 MB for wasmtinygo..gobytes, measured with
// `go tool nm -size` on an unstripped android/arm64 build — which is what put
// libskywire-mobile.so 4.7 MB over its size budget.
//
// The package API is built for this case: Embedded() reports false, Has() is
// false for every variant, Available() is empty and GetVariant() returns its
// "not embedded in this build" error. Same shape as pkg/geoip, whose 29 MB
// GeoLite2 embed is likewise stripped from mobile (geoip_embedded.go is
// !mobile, with a stub alongside).
// blob is a gzipped wasm-visor paired with its matching wasm_exec.js loader.
type blob struct {
	gz     []byte
	execJS []byte
}

var (
	variants       = map[Variant]blob{}
	defaultVariant = Go
)
