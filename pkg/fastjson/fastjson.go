// Package fastjson is the JSON codec shared by packages that bind the name
// json to something else. It is encoding/json under both Go and TinyGo.
package fastjson

import (
	stdjson "encoding/json"
	"io"
)

// JSON is the codec.
var JSON = codec{}

type codec struct{}

func (codec) Marshal(v any) ([]byte, error)           { return stdjson.Marshal(v) }
func (codec) Unmarshal(b []byte, v any) error         { return stdjson.Unmarshal(b, v) }
func (codec) NewDecoder(r io.Reader) *stdjson.Decoder { return stdjson.NewDecoder(r) }
func (codec) NewEncoder(w io.Writer) *stdjson.Encoder { return stdjson.NewEncoder(w) }
func (codec) MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return stdjson.MarshalIndent(v, prefix, indent)
}
