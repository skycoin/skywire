package msg

import (
	"bytes"
	"testing"
)

// Decode must leave the input untouched and agree with the copying decoder.
func TestDecodeDoesNotModifyInput(t *testing.T) {
	enc := (&Object{Value: bytes.Repeat([]byte{0x5a}, 4096)}).Encode()
	in := append([]byte(nil), enc...)
	m, err := Decode(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, enc) {
		t.Fatal("Decode modified its input")
	}
	if !bytes.Equal(m.(*Object).Value, bytes.Repeat([]byte{0x5a}, 4096)) {
		t.Fatal("value differs")
	}
}

func BenchmarkObjectDecode(b *testing.B) {
	enc := (&Object{Value: make([]byte, 64<<10)}).Encode()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Decode(enc); err != nil {
			b.Fatal(err)
		}
	}
}
