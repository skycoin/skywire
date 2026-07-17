//go:build tinygo

// Package httputil pkg/httputil/json_tinygo.go c0-com-http
//
// TinyGo build of the package-wide `json` codec. The native build uses
// jsoniter.ConfigFastest (json_native.go), which drags in
// github.com/modern-go/reflect2 — its unsafe reflection (reflect.mapassign /
// unsafe_New / typedmemmove / makemap …) has no implementation in TinyGo's
// runtime and fails to link. The standard library's encoding/json compiles and
// runs under TinyGo, so wrap it in a tiny value exposing just the
// NewEncoder/NewDecoder that httputil uses.
package httputil

import (
	stdjson "encoding/json"
	"io"
)

var json = jsonCodec{}

type jsonCodec struct{}

func (jsonCodec) NewEncoder(w io.Writer) *stdjson.Encoder { return stdjson.NewEncoder(w) }
func (jsonCodec) NewDecoder(r io.Reader) *stdjson.Decoder { return stdjson.NewDecoder(r) }
