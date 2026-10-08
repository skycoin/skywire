package transport

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/skycoin/skywire/pkg/routing"
)

func TestPacketDecoderSplitsAnyChunking(t *testing.T) {
	rng := rand.New(rand.NewSource(2)) //nolint:gosec
	var want []routing.Packet
	var stream []byte
	for i := 0; i < 500; i++ {
		payload := make([]byte, rng.Intn(3000)) // includes empty payloads
		rng.Read(payload)                       //nolint:errcheck
		p, err := routing.MakeDataPacket(routing.RouteID(i), payload)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, p)
		stream = append(stream, p...)
	}

	for _, maxChunk := range []int{1, 7, 64, 1500, len(stream)} {
		var d packetDecoder
		var got []routing.Packet
		emit := func(p routing.Packet) { got = append(got, p) }
		for rest := stream; len(rest) > 0; {
			n := min(1+rng.Intn(maxChunk), len(rest))
			d.feed(rest[:n], emit)
			rest = rest[n:]
		}
		if len(got) != len(want) {
			t.Fatalf("chunks up to %d: %d packets, want %d", maxChunk, len(got), len(want))
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("chunks up to %d: packet %d differs", maxChunk, i)
			}
		}
	}
}
