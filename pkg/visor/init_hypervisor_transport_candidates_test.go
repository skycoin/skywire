// Package visor pkg/visor/init_hypervisor_transport_candidates_test.go c3-vis-core
package visor

import (
	"testing"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func knownSet(ts ...tptypes.Type) func(tptypes.Type) bool {
	m := make(map[tptypes.Type]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return func(t tptypes.Type) bool { return m[t] }
}

func eq(a, b []tptypes.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The upgrade used to gate on stcpr/sudph only, which excluded every visor
// that has neither — a browser visor opens swtr/swsr/webrtc and nothing
// else, so it never got off the dmsg relay. Selection must follow the
// preference order for whatever the visor can actually open.
func TestDirectCarrierCandidates(t *testing.T) {
	order := tptypes.PreferenceOrder()

	t.Run("browser visor gets its own carriers, not none", func(t *testing.T) {
		got := directCarrierCandidates(order, knownSet(tptypes.WS, tptypes.WEBRTC, tptypes.DMSG))
		if len(got) == 0 {
			t.Fatal("no candidates for a browser visor: it would never leave the dmsg relay")
		}
		if !eq(got, []tptypes.Type{tptypes.WS, tptypes.WEBRTC}) {
			t.Errorf("got %v, want [ws webrtc] in preference order", got)
		}
	})

	t.Run("dmsg is never a candidate", func(t *testing.T) {
		for _, c := range directCarrierCandidates(order, func(tptypes.Type) bool { return true }) {
			if c == tptypes.DMSG {
				t.Fatal("dmsg offered as an upgrade target; it is the relay being escaped")
			}
		}
	})

	t.Run("preference order is preserved", func(t *testing.T) {
		got := directCarrierCandidates(order, knownSet(tptypes.WEBRTC, tptypes.STCPR, tptypes.SUDPH))
		if !eq(got, []tptypes.Type{tptypes.STCPR, tptypes.SUDPH, tptypes.WEBRTC}) {
			t.Errorf("got %v, want [stcpr sudph webrtc]", got)
		}
	})

	t.Run("pure-dmsg visor yields nothing", func(t *testing.T) {
		if got := directCarrierCandidates(order, knownSet(tptypes.DMSG)); len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}
