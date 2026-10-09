package transport

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// MakeTransportID must keep producing the IDs every visor and TPD already hold.
func TestMakeTransportIDMatchesNewHash(t *testing.T) {
	ref := func(a, b cipher.PubKey, tp types.Type) uuid.UUID {
		keys := SortEdges(a, b)
		data := append(append(append([]byte{}, keys[0][:]...), keys[1][:]...), string(tp)...)
		return uuid.NewHash(sha256.New(), uuid.UUID{}, data, 0)
	}
	tps := append(types.Known(), "", types.Type(strings.Repeat("x", 40)))
	for i := 0; i < 500; i++ {
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		for _, tp := range tps {
			if got, want := MakeTransportID(a, b, tp), ref(a, b, tp); got != want {
				t.Fatalf("MakeTransportID(%s, %s, %q) = %s, want %s", a, b, tp, got, want)
			}
		}
	}
}

func BenchmarkMakeTransportID(b *testing.B) {
	x, _ := cipher.GenerateKeyPair()
	y, _ := cipher.GenerateKeyPair()
	b.ReportAllocs()
	for b.Loop() {
		MakeTransportID(x, y, types.STCPR)
	}
}
