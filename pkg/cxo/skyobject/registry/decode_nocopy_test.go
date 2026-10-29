package registry

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/skycoin/skycoin/src/cipher/encoder"
)

type plainObj struct {
	Name string
	N    uint64
	Hs   [4]byte
	L    []uint32
}

type bytesObj struct {
	Name string
	Data []byte
}

func TestHasByteSlice(t *testing.T) {
	if hasByteSlice(reflect.TypeOf(plainObj{}), map[reflect.Type]bool{}) {
		t.Fatal("plainObj has no byte slice")
	}
	if !hasByteSlice(reflect.TypeOf(bytesObj{}), map[reflect.Type]bool{}) {
		t.Fatal("bytesObj has a byte slice")
	}
}

// A byte slice field must not point into the input that stays shared.
func TestDecodeCopiesByteSlices(t *testing.T) {
	enc := encoder.Serialize(&bytesObj{Name: "a", Data: []byte{1, 2, 3}})
	var o bytesObj
	if _, err := decode(enc, &o); err != nil {
		t.Fatal(err)
	}
	o.Data[0] = 9
	var p bytesObj
	if _, err := decode(enc, &p); err != nil || !bytes.Equal(p.Data, []byte{1, 2, 3}) {
		t.Fatalf("input was modified through the decoded value: %v %v", p.Data, err)
	}
}

func TestDecodePlainLeavesInput(t *testing.T) {
	enc := encoder.Serialize(&plainObj{Name: "abc", N: 7, L: []uint32{1, 2}})
	in := append([]byte(nil), enc...)
	var o plainObj
	if _, err := decode(in, &o); err != nil {
		t.Fatal(err)
	}
	o.Name = "zzz"
	o.L[0] = 99
	if !bytes.Equal(in, enc) {
		t.Fatal("input modified")
	}
}

func BenchmarkDecodePlain(b *testing.B) {
	l := make([]uint32, 16<<10)
	enc := encoder.Serialize(&plainObj{Name: "abc", L: l})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var o plainObj
		if _, err := decode(enc, &o); err != nil {
			b.Fatal(err)
		}
	}
}
