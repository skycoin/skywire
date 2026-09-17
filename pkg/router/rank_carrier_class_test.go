//go:build !tinygo || (js && wasm)

package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// carrierCandPath builds a two-hop forward candidate whose FIRST hop is a real
// transport of the named type — the id has to be the derivable one, since that
// is how the ranking learns a hop's carrier (transport.TypeFromTransportID).
// The second hop is measured at rankCandTailMs so the candidate is known end to
// end and only the first hop varies.
func carrierCandPath(t tptypes.Type) []routing.Hop {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	return []routing.Hop{
		{TpID: transport.MakeTransportID(src, mid, t), From: src, To: mid},
		{TpID: uuid.New(), From: mid, To: dst, Latency: rankCandTailMs},
	}
}

// A hole-punched UDP first hop does not outrank a resolved TCP one by being
// faster to answer a ping. Measured on the rig 2026-09-17 (campaign21,
// mux-tunnels-2-up2): the ranked sibling dial took a sudph fleet hop as the
// second ACTIVE tunnel and the pool filled five of six standby slots with more
// of them, because their first-hop latencies beat the rig's stcpr
// intermediates — and two concurrent 50 MB uploads then measured 8.6 + 0.5
// MB/s against references of 10.1 + 8.8, three trials of three.
func TestRankByPathLatency_CarrierClassBeatsLatency(t *testing.T) {
	sudph := carrierCandPath(tptypes.SUDPH)
	stcpr := carrierCandPath(tptypes.STCPR)
	lat := map[uuid.UUID]float64{
		sudph[0].TpID: 20, // the fast one...
		stcpr[0].TpID: 60, // ...and the one that actually carries traffic
	}
	latencyFor := func(id uuid.UUID) float64 { return lat[id] }

	ranked := rankByPathLatency([][]routing.Hop{sudph, stcpr}, latencyFor)
	require.Equal(t, stcpr[0].TpID, ranked[0][0].TpID,
		"a 60ms stcpr candidate must outrank a 20ms sudph one")
	require.Len(t, ranked, 2, "this is an ordering, not a filter: the sudph route is still dialable and still poolable")
}

// The whole order, and what it means: direct TCP/QUIC, then hole-punched UDP,
// then webrtc, then dmsg and anything unrecognized. Latencies here are
// deliberately the reverse of the wanted order, so only the class can explain
// the answer.
func TestRankByPathLatency_CarrierClassOrder(t *testing.T) {
	dmsg := carrierCandPath(tptypes.DMSG)
	webrtc := carrierCandPath(tptypes.WEBRTC)
	sudph := carrierCandPath(tptypes.SUDPH)
	squicr := carrierCandPath(tptypes.QUIC)
	lat := map[uuid.UUID]float64{
		dmsg[0].TpID:   10,
		webrtc[0].TpID: 20,
		sudph[0].TpID:  30,
		squicr[0].TpID: 40,
	}
	latencyFor := func(id uuid.UUID) float64 { return lat[id] }

	ranked := rankByPathLatency([][]routing.Hop{dmsg, webrtc, sudph, squicr}, latencyFor)
	require.Equal(t, []uuid.UUID{squicr[0].TpID, sudph[0].TpID, webrtc[0].TpID, dmsg[0].TpID},
		firstTpIDs(ranked))
}

// Within a class latency still decides, and an unmeasured candidate is last —
// in its OWN class. A measured sudph route does not climb above an unmeasured
// stcpr one: an unknown link is not evidence of a bad one, and the class is
// what the live numbers were about.
func TestRankByPathLatency_LatencyAndUnmeasuredWithinAClass(t *testing.T) {
	fast := carrierCandPath(tptypes.STCPR)
	slow := carrierCandPath(tptypes.STCPR)
	unmeasured := carrierCandPath(tptypes.STCPR)
	sudph := carrierCandPath(tptypes.SUDPH)
	lat := map[uuid.UUID]float64{
		fast[0].TpID:  39,
		slow[0].TpID:  470,
		sudph[0].TpID: 5,
	}
	latencyFor := func(id uuid.UUID) float64 { return lat[id] }

	ranked := rankByPathLatency([][]routing.Hop{slow, unmeasured, sudph, fast}, latencyFor)
	require.Equal(t, []uuid.UUID{fast[0].TpID, slow[0].TpID, unmeasured[0].TpID, sudph[0].TpID},
		firstTpIDs(ranked))
}

// A candidate whose carrier cannot be derived — every existing fixture, and any
// transport id this visor did not build — classes with the others, so the
// ranking degrades to the latency order it had before rather than to an
// arbitrary one.
func TestFirstHopCarrierClass_UnknownAndEmpty(t *testing.T) {
	require.Equal(t, carrierClassOther, firstHopCarrierClass(nil))
	require.Equal(t, carrierClassOther, firstHopCarrierClass(rankCandPath(uuid.New(), 2)))
	require.Equal(t, carrierClassDirect, firstHopCarrierClass(carrierCandPath(tptypes.STCP)))
}
