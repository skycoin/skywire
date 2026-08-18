package rpcgrpc

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestMuxBwTransportCostMs pins the per-hop ranking cost: the transport-type
// prior orders stcpr/quic best and webrtc/dmsg worst, but a MEASURED throughput
// estimate overrides the type entirely — a fast webrtc link beats a slow stcpr
// one. Mirrors pkg/router.transportCostMs (PR #3989).
func TestMuxBwTransportCostMs(t *testing.T) {
	// Type prior (no measurement).
	cases := []struct {
		tp   string
		want float64
	}{
		{"stcpr", 0}, {"squicr", 0}, {"sudph", 25}, {"stcp", 50},
		{"webrtc", 300}, {"dmsg", 400}, {"mystery", 100},
	}
	for _, c := range cases {
		if got := muxBwTransportCostMs(c.tp, 0); got != c.want {
			t.Errorf("muxBwTransportCostMs(%q, 0) = %v, want %v", c.tp, got, c.want)
		}
	}

	// Measured overrides type: a webrtc link measured fast scores 0, beating a
	// stcpr link measured slow (400).
	if fast := muxBwTransportCostMs("webrtc", 40_000_000); fast != 0 {
		t.Errorf("measured-fast webrtc = %v, want 0", fast)
	}
	if slow := muxBwTransportCostMs("stcpr", 30_000); slow != 400 {
		t.Errorf("measured-slow stcpr = %v, want 400", slow)
	}
	if muxBwTransportCostMs("webrtc", 40_000_000) >= muxBwTransportCostMs("stcpr", 30_000) {
		t.Error("a measured-fast webrtc leg must rank ahead of a measured-slow stcpr leg")
	}
}

// TestMuxBwRouteCostMs verifies the route cost sums per-hop costs, so a route
// over webrtc is ranked worse than an otherwise-equal route over stcpr — which
// is what steers disjoint mux-leg selection away from webrtc.
func TestMuxBwRouteCostMs(t *testing.T) {
	idA, idB := uuid.New(), uuid.New()
	tpTypes := map[string]string{idA.String(): "stcpr", idB.String(): "webrtc"}
	tpThroughput := map[string]float64{} // no measurements → type prior

	stcprRoute := []routing.Hop{{TpID: idA}}
	webrtcRoute := []routing.Hop{{TpID: idB}}

	cStcpr := muxBwRouteCostMs(stcprRoute, tpTypes, tpThroughput)
	cWebrtc := muxBwRouteCostMs(webrtcRoute, tpTypes, tpThroughput)
	if cStcpr != 0 {
		t.Errorf("stcpr route cost = %v, want 0", cStcpr)
	}
	if cWebrtc != 300 {
		t.Errorf("webrtc route cost = %v, want 300", cWebrtc)
	}
	if cStcpr >= cWebrtc {
		t.Errorf("stcpr route (%v) must rank cheaper than webrtc route (%v)", cStcpr, cWebrtc)
	}

	// A measured-fast webrtc hop flips the ranking: evidence beats the prior.
	tpThroughput[idB.String()] = 40_000_000
	if muxBwRouteCostMs(webrtcRoute, tpTypes, tpThroughput) != 0 {
		t.Error("measured-fast webrtc route should cost 0")
	}
}
