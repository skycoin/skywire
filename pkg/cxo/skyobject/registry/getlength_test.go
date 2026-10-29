package registry

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// getLengthOld is the reflective read getLength replaced.
func getLengthOld(p []byte) (int, error) {
	var u uint32
	_, err := encoder.DeserializeRaw(p, &u)
	return int(u), err
}

func TestGetLengthMatchesDeserializeRaw(t *testing.T) {
	for _, p := range [][]byte{
		nil, {}, {1}, {1, 2, 3},
		{0, 0, 0, 0}, {1, 0, 0, 0}, {0xff, 0xff, 0xff, 0xff},
		{0x78, 0x56, 0x34, 0x12, 9, 9, 9},
		encoder.Serialize([]string{"a", "bc"}),
	} {
		want, werr := getLengthOld(p)
		got, gerr := getLength(p)
		if want != got || werr != gerr {
			t.Fatalf("%x: got %d, %v; want %d, %v", p, got, gerr, want, werr)
		}
	}
}

// BenchmarkGetLength reads the length prefix of a 64 kB slice value.
func BenchmarkGetLength(b *testing.B) {
	p := encoder.Serialize(make([]byte, 64<<10))
	for name, fn := range map[string]func([]byte) (int, error){"Old": getLengthOld, "New": getLength} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn(p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
