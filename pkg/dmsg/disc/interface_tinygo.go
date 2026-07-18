//go:build tinygo

// Package disc pkg/dmsg/disc/interface_tinygo.go c1-net-dmsg
//
// TinyGo build of the package-wide `json` codec. The native build uses
// jsoniter.ConfigFastest (interface_native.go); under TinyGo we use the
// standard library's encoding/json, which compiles under TinyGo 0.41 (the
// reflect helpers it needs are now shipped — unlike when the jsoniter split was
// introduced). Wrapped in a tiny value so `json.Marshal` / `json.Unmarshal`
// read identically to the jsoniter path, letting Sign / VerifySignature compile
// for a TinyGo dmsg client (which registers + signs its own discovery entry).
// See docs/design/tinygo-dmsg-client.md.
package disc

import (
	stdjson "encoding/json"
	"io"
)

var json = jsonCodec{}

type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error)   { return stdjson.Marshal(v) }
func (jsonCodec) Unmarshal(b []byte, v interface{}) error { return stdjson.Unmarshal(b, v) }
func (jsonCodec) NewDecoder(r io.Reader) *stdjson.Decoder { return stdjson.NewDecoder(r) }
func (jsonCodec) NewEncoder(w io.Writer) *stdjson.Encoder { return stdjson.NewEncoder(w) }
