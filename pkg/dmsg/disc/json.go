// Package disc pkg/dmsg/disc/json.go c1-net-dmsg
package disc

import (
	"bytes"
	stdjson "encoding/json"
	"io"
)

// json is the package codec, encoding/json under both Go and TinyGo.
var json = jsonCodec{}

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error)           { return stdjson.Marshal(v) }
func (jsonCodec) Unmarshal(b []byte, v any) error         { return stdjson.Unmarshal(b, v) }
func (jsonCodec) NewDecoder(r io.Reader) *stdjson.Decoder { return stdjson.NewDecoder(r) }
func (jsonCodec) NewEncoder(w io.Writer) *stdjson.Encoder { return stdjson.NewEncoder(w) }

// signedPayload is the byte form of v that an Entry signature covers. It does
// not escape <, > and &, so it matches the jsoniter.ConfigFastest bytes that
// Go builds signed before they moved to encoding/json.
func signedPayload(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := stdjson.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}
