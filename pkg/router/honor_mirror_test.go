package router

import "testing"

// TestHonorsMirrorActiveSet: only the ACCEPTOR (initiator==false) with leg-state
// signaling negotiated defers its active set to the peer's mirror. This is what
// stops the acceptor's latency-band / bottleneck controllers from re-admitting
// legs the initiator parked (the wide-mux download over-subscription).
func TestHonorsMirrorActiveSet(t *testing.T) {
	cases := []struct {
		name       string
		mux        *routeMux
		initiator  bool
		wantHonors bool
	}{
		{"acceptor-with-signaling", &routeMux{legStateEnabled: true}, false, true},
		{"initiator-with-signaling", &routeMux{legStateEnabled: true}, true, false},
		{"acceptor-no-signaling", &routeMux{legStateEnabled: false}, false, false},
		{"initiator-no-signaling", &routeMux{legStateEnabled: false}, true, false},
		{"nil-mux", nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rg := &RouteGroup{mux: c.mux, initiator: c.initiator}
			if got := rg.honorsMirrorActiveSet(); got != c.wantHonors {
				t.Fatalf("honorsMirrorActiveSet = %v, want %v", got, c.wantHonors)
			}
		})
	}
}
