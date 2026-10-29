// Package httputil pkg/httputil/json.go c0-com-http
package httputil

import (
	stdjson "encoding/json"
	"io"
)

// json is the package codec, encoding/json under both Go and TinyGo.
var json = jsonCodec{}

type jsonCodec struct{}

func (jsonCodec) NewEncoder(w io.Writer) *stdjson.Encoder { return stdjson.NewEncoder(w) }
func (jsonCodec) NewDecoder(r io.Reader) *stdjson.Decoder { return stdjson.NewDecoder(r) }
func (jsonCodec) Marshal(v any) ([]byte, error)           { return stdjson.Marshal(v) }
func (jsonCodec) MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return stdjson.MarshalIndent(v, prefix, indent)
}
