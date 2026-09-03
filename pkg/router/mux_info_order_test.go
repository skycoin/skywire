package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestSortMuxInfosStable pins the deterministic ordering that keeps the status
// tree / CLI mux-info rows from reshuffling: same PKs, distinct ports must come
// back in ascending (dst_port, src_port) order regardless of input order.
func TestSortMuxInfosStable(t *testing.T) {
	// All tunnels of one proxy share src/dst PK and differ only by port, so one
	// valid keypair each is enough — the sort key that matters here is the port.
	src, _, err := cipher.GenerateDeterministicKeyPair([]byte("mux-order-src"))
	if err != nil {
		t.Fatal(err)
	}
	dst, _, err := cipher.GenerateDeterministicKeyPair([]byte("mux-order-dst"))
	if err != nil {
		t.Fatal(err)
	}

	mk := func(dstPort, srcPort routing.Port) MuxInfo {
		return MuxInfo{Desc: routing.NewRouteDescriptor(src, dst, srcPort, dstPort)}
	}
	// scrambled input
	in := []MuxInfo{mk(49160, 3), mk(49157, 3), mk(49159, 3), mk(49158, 3)}
	sortMuxInfos(in)

	want := []routing.Port{49157, 49158, 49159, 49160}
	for i, w := range want {
		if got := in[i].Desc.DstPort(); got != w {
			t.Fatalf("pos %d: dst_port=%d want %d (order=%v)", i, got, w,
				[]routing.Port{in[0].Desc.DstPort(), in[1].Desc.DstPort(), in[2].Desc.DstPort(), in[3].Desc.DstPort()})
		}
	}
	// idempotent: sorting again yields the same order
	sortMuxInfos(in)
	if in[0].Desc.DstPort() != 49157 || in[3].Desc.DstPort() != 49160 {
		t.Fatal("sort not idempotent")
	}
}
