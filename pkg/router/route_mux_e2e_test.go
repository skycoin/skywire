package router

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// These tests exercise the mux data-plane reassembly logic (routeMux +
// reorderBuffer + SACK/retx) in isolation — the "stream-reassembly across legs"
// layer that stalls live at routes>=2. They are the deterministic debugging aid
// for that layer: a sender routeMux wraps a byte stream into sequenced packets,
// those packets are handed to a receiver routeMux's deliverData in various
// arrival orders (in-order, interleaved-across-legs, with loss), and we assert
// the receiver reassembles the ORIGINAL ordered stream with no gaps.

func newTestMux(t *testing.T, legs int) *routeMux {
	t.Helper()
	m := newRouteMux(logging.MustGetLogger("muxtest"), true)
	m.growLegs(legs)
	return m
}

// collect drains deliverData's returned payloads into a flat ordered slice.
func collect(dst *[]byte, delivered [][]byte) {
	for _, d := range delivered {
		*dst = append(*dst, d...)
	}
}

func TestMux_InOrderDelivery(t *testing.T) {
	send := newTestMux(t, 2)
	recv := newTestMux(t, 2)

	var got []byte
	for i := 0; i < 200; i++ {
		payload := []byte{byte(i)}
		_, seq, err := send.wrapPayload(routing.RouteID(1), payload)
		if err != nil {
			t.Fatal(err)
		}
		delivered, _ := recv.deliverData(seq, payload)
		collect(&got, delivered)
	}
	if len(got) != 200 {
		t.Fatalf("in-order: delivered %d/200 bytes — reassembly stalled", len(got))
	}
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("in-order: byte %d = %d, want %d", i, got[i], byte(i))
		}
	}
}

// TestMux_InterleavedLegs simulates two legs delivering packets out of order
// (the whole point of mux): odd seqs "arrive" on leg B slightly after even seqs
// on leg A. The reorder buffer must still reassemble the original order.
func TestMux_InterleavedLegs(t *testing.T) {
	send := newTestMux(t, 2)
	recv := newTestMux(t, 2)

	type pkt struct {
		seq  uint32
		data []byte
	}
	var evens, odds []pkt
	for i := 0; i < 200; i++ {
		payload := []byte{byte(i)}
		_, seq, err := send.wrapPayload(routing.RouteID(1), payload)
		if err != nil {
			t.Fatal(err)
		}
		if seq%2 == 0 {
			evens = append(evens, pkt{seq, payload})
		} else {
			odds = append(odds, pkt{seq, payload})
		}
	}
	// Deliver in a leg-interleaved order: a batch of evens, then a batch of
	// odds, so each odd seq arrives while its predecessor even is already in.
	var got []byte
	ei, oi := 0, 0
	for ei < len(evens) || oi < len(odds) {
		for b := 0; b < 4 && ei < len(evens); b++ {
			d, _ := recv.deliverData(evens[ei].seq, evens[ei].data)
			collect(&got, d)
			ei++
		}
		for b := 0; b < 4 && oi < len(odds); b++ {
			d, _ := recv.deliverData(odds[oi].seq, odds[oi].data)
			collect(&got, d)
			oi++
		}
	}
	if len(got) != 200 {
		t.Fatalf("interleaved: delivered %d/200 bytes — reorder stalled", len(got))
	}
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("interleaved: byte %d = %d, want %d (reorder mis-sequenced)", i, got[i], byte(i))
		}
	}
}

// TestMux_RecoversLostPacketViaRetx models the reliability contract: one packet
// is "lost" in transit, the receiver's SACK reports the gap, and the sender
// retransmits the lost payload from its retx buffer. After the retransmit the
// receiver must deliver the full ordered stream. If this fails, the SACK/retx
// recovery loop is broken — which is the live routes>=2 stall.
func TestMux_RecoversLostPacketViaRetx(t *testing.T) {
	send := newTestMux(t, 2)
	recv := newTestMux(t, 2)

	// Wrap 20 payloads; drop seq==5 on first pass.
	type pkt struct {
		seq  uint32
		data []byte
	}
	var pkts []pkt
	for i := 0; i < 20; i++ {
		payload := []byte{byte(i)}
		_, seq, err := send.wrapPayload(routing.RouteID(1), payload)
		if err != nil {
			t.Fatal(err)
		}
		pkts = append(pkts, pkt{seq, payload})
	}

	var got []byte
	const lost = uint32(5)
	for _, p := range pkts {
		if p.seq == lost {
			continue // simulate loss
		}
		d, _ := recv.deliverData(p.seq, p.data)
		collect(&got, d)
	}
	// At this point only seqs 0..4 are deliverable; 6..19 are buffered behind
	// the gap at seq 5. The receiver should report the gap via SACK.
	lastContig, _ := recv.generateSACK()
	if lastContig >= lost {
		t.Fatalf("SACK lastContig=%d did not stop before the lost seq %d", lastContig, lost)
	}

	// Sender must still hold the lost payload for retransmission.
	retx := send.getRetxPayload(lost)
	if retx == nil {
		t.Fatalf("retx buffer dropped lost seq %d — sender cannot recover it", lost)
	}
	// Retransmit it.
	d, _ := recv.deliverData(lost, retx)
	collect(&got, d)

	if len(got) != 20 {
		t.Fatalf("after retx: delivered %d/20 bytes — SACK/retx recovery is broken (this is the live routes>=2 stall)", len(got))
	}
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("after retx: byte %d = %d, want %d", i, got[i], byte(i))
		}
	}
}

// --- send-driving tests (using the createMuxRouteGroup harness) -------------
// These exercise the SEND path (RouteGroup.Write -> nextTransport ->
// selectTransport -> rg.write) with 2 legs, reproducing the live scenario
// where an aux leg starts NOT ready. The send must never stall: it must fall
// back to the always-ready primary leg 0.

func TestMux_SendDrivingWithUnreadyAuxLeg(t *testing.T) {
	rg, _, conns := createMuxRouteGroup(t, 2)
	defer func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck
		}
	}()

	// Live scenario: aux leg (index 1) not yet proven-ready by inbound traffic.
	rg.mu.Lock()
	rg.mux.ready[1] = false
	rg.mu.Unlock()

	_ = rg.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	for i := 0; i < 300; i++ {
		n, err := rg.Write([]byte{byte(i)})
		if err != nil {
			t.Fatalf("write %d STALLED/errored with an unready aux leg: %v (send-driving falls over instead of using ready leg 0)", i, err)
		}
		if n != 1 {
			t.Fatalf("write %d: n=%d want 1", i, n)
		}
	}
}

func TestMux_SendDistributesAcrossReadyLegs(t *testing.T) {
	rg, _, conns := createMuxRouteGroup(t, 2) // both legs marked ready by helper
	defer func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck
		}
	}()

	_ = rg.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	for i := 0; i < 400; i++ {
		if _, err := rg.Write([]byte{byte(i)}); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}
	s0 := atomic.LoadUint64(&rg.mux.legs[0].sentBytes)
	s1 := atomic.LoadUint64(&rg.mux.legs[1].sentBytes)
	if s0 == 0 || s1 == 0 {
		t.Fatalf("traffic not split across both ready legs: leg0=%d leg1=%d (mux funneled to one)", s0, s1)
	}
}
