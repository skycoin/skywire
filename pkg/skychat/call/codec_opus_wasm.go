//go:build js && wasm

// Package call pkg/skychat/call/codec_opus_wasm.go c4-app-chat
//
// Browser build: no pure-Go Opus. github.com/thesyncim/gopus ships ~200 KB of
// static CELT lookup tables (celt.pulseCacheLookup50 et al.) as package-level
// arrays. Under TinyGo, interp materialises those inline into runtime.initAll —
// ~200k instructions in one function — which overflows LLVM's WebAssembly
// instruction selector, and even under standard Go js/wasm it bloats the blob
// for a codec the browser doesn't need: a browser tab does Opus via WebCodecs,
// not a Go implementation. NewOpusCodec therefore reports unavailable so the
// voice manager falls back to the raw-PCM codec (see visor init_voice.go), which
// is the wire-compatible baseline until a WebCodecs-backed codec is wired in.
package call

import "errors"

// NewOpusCodec is unavailable in the browser build; callers fall back to PCM.
func NewOpusCodec() (Codec, error) {
	return nil, errors.New("opus codec unavailable in the browser build (use WebCodecs or PCM)")
}
