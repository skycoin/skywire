package msg

import (
	"bytes"
	"testing"
)

// Object's direct encoding must match the reflect encoder byte for byte.
func TestObjectEncodeMatchesSerialize(t *testing.T) {
	for _, n := range []int{0, 1, 100, 70 << 10} {
		o := &Object{Value: bytes.Repeat([]byte{0xa5}, n)}
		if got, want := o.Encode(), encode(o); !bytes.Equal(got, want) {
			t.Fatalf("len %d: Encode differs from encode", n)
		}
		m, err := Decode(o.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if got := m.(*Object).Value; len(got) != n || (n > 0 && !bytes.Equal(got, o.Value)) {
			t.Fatalf("len %d: round trip differs", n)
		}
	}
	if !bytes.Equal((&Object{}).Encode(), encode(&Object{Value: nil})) {
		t.Fatal("nil value differs")
	}
}

func BenchmarkObjectEncode(b *testing.B) {
	o := &Object{Value: make([]byte, 256<<10)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = o.Encode()
	}
}
