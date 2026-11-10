// Package router pkg/router/leg_identity_test.go c2-net-routing
package router

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// legIdxOf is the current index of the leg on mt, or -1.
func legIdxOf(rg *RouteGroup, mt *transport.ManagedTransport) int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for i, lg := range rg.legs {
		if lg.tp == mt {
			return i
		}
	}
	return -1
}

// legParked reports whether the leg on mt is in warm standby.
func legParked(t *testing.T, rg *RouteGroup, mt *transport.ManagedTransport) bool {
	t.Helper()
	idx := legIdxOf(rg, mt)
	require.GreaterOrEqual(t, idx, 0, "the leg must still be in the group")
	return rg.mux.isLegStandby(idx)
}

func isLegState(b []byte) bool {
	return len(b) > routing.PacketTypeOffset && routing.Packet(b).Type() == routing.LegStatePacket
}

// pruneOnLegState removes the leg on victim during the first leg-state write
// on conn, which is where a keep-alive prune lands between two loop steps.
func pruneOnLegState(rg *RouteGroup, conn *workingTransport, victim *transport.ManagedTransport) {
	var once sync.Once
	conn.onWrite = func(b []byte) {
		if !isLegState(b) {
			return
		}
		once.Do(func() { rg.pruneDeadLegs([]uuid.UUID{victim.Entry.ID}, "test", "test") })
	}
}

// countLegState records the leg-state packets written on conn.
func countLegState(conn *workingTransport) *int {
	n := new(int)
	prev := conn.onWrite
	conn.onWrite = func(b []byte) {
		if isLegState(b) {
			*n++
		}
		if prev != nil {
			prev(b)
		}
	}
	return n
}

func setLegLatencies(rg *RouteGroup, mts []*transport.ManagedTransport, ms ...float64) {
	rg.legLivenessMu.Lock()
	defer rg.legLivenessMu.Unlock()
	if rg.legE2ELatency == nil {
		rg.legE2ELatency = map[uuid.UUID]float64{}
	}
	for i, v := range ms {
		rg.legE2ELatency[mts[i].Entry.ID] = v
	}
}

// TestLatencyBandParksTheLegItJudged removes a leg while the band is mirroring
// its first park. The second park must still land on the leg it was decided for.
func TestLatencyBandParksTheLegItJudged(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 5)
	rg.mux.legStateEnabled = true
	rg.initiator = true
	setLegLatencies(rg, mts, 150, 2, 155, 2, 145)
	pruneOnLegState(rg, conns[1], mts[2])
	mirrored := countLegState(conns[4])

	rg.enforceLatencyBand(nil)

	require.True(t, legParked(t, rg, mts[1]))
	require.False(t, legParked(t, rg, mts[4]), "the in-band leg must stay active")
	require.True(t, legParked(t, rg, mts[3]), "the out-of-band leg must be parked")
	require.Zero(t, *mirrored, "no park may be mirrored on the in-band leg")
}

// TestStalledParkLandsOnTheStalledLeg is the same interleaving on the
// data-progress park.
func TestStalledParkLandsOnTheStalledLeg(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 5)
	rg.mux.legStateEnabled = true
	rg.initiator = true
	pruneOnLegState(rg, conns[1], mts[2])
	mirrored := countLegState(conns[4])

	rg.demoteStalledLegs([]uuid.UUID{mts[1].Entry.ID, mts[3].Entry.ID})

	require.False(t, legParked(t, rg, mts[4]), "the healthy leg must stay active")
	require.True(t, legParked(t, rg, mts[3]), "the stalled leg must be parked")
	require.Zero(t, *mirrored, "no park may be mirrored on the healthy leg")
}

// TestBottleneckParkLandsOnTheGroupedLeg is the same interleaving on the
// shared-bottleneck park. Legs 0 to 2 share a delay signature and leg 3 does not.
func TestBottleneckParkLandsOnTheGroupedLeg(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 4)
	rg.muxEvents = &muxEventRing{}
	rg.mux.legStateEnabled = true
	rg.initiator = true
	sbdDemoteOn(t)
	ids := make([]uuid.UUID, len(mts))
	rg.legLivenessMu.Lock()
	for i, mt := range mts {
		ids[i] = mt.Entry.ID
		rg.legE2ELatency[mt.Entry.ID] = 50
		if i == 3 {
			continue
		}
		w := newSBDWindow()
		for _, s := range []float64{50, 62, 50, 62, 50, 62, 50, 62} {
			w.push(s)
		}
		rg.legOWD[mt.Entry.ID] = w
	}
	rg.legLivenessMu.Unlock()
	pruneOnLegState(rg, conns[1], mts[1])

	sbdTick(rg, ids, 25_000_000, 21_500_000, 21_500_000, 21_500_000)

	require.False(t, legParked(t, rg, mts[3]), "the leg outside the bottleneck group must stay active")
	require.True(t, legParked(t, rg, mts[2]), "the co-bottlenecked leg must be parked")
}

// tickHook is a rotation hook that loses a leg while the policy runs.
type tickHook struct {
	rg     *RouteGroup
	victim *transport.ManagedTransport
	act    RotationAction
}

func (h *tickHook) OnTick(DialInfo, []LegInfo) RotationAction {
	h.rg.pruneDeadLegs([]uuid.UUID{h.victim.Entry.ID}, "test", "test")
	return h.act
}

func rotateWith(rg *RouteGroup, victim *transport.ManagedTransport, act RotationAction) {
	rg.mu.Lock()
	rg.rotationHook = &tickHook{rg: rg, victim: victim, act: act}
	rg.mu.Unlock()
	rg.rotationServiceFn(0)
}

// TestRotationActsOnTheLegsThePolicySaw removes leg 1 while on_tick runs. The
// indices on_tick returns name legs of the snapshot it was shown.
func TestRotationActsOnTheLegsThePolicySaw(t *testing.T) {
	t.Run("demote", func(t *testing.T) {
		rg, mts, _ := createMuxRouteGroup(t, 4)
		rotateWith(rg, mts[1], RotationAction{DemoteToStandby: []int{2}})
		require.False(t, legParked(t, rg, mts[3]), "the leg the policy did not name must stay active")
		require.True(t, legParked(t, rg, mts[2]), "the leg the policy named must be parked")
	})
	t.Run("promote", func(t *testing.T) {
		rg, mts, _ := createMuxRouteGroup(t, 4)
		rg.muxEvents = &muxEventRing{}
		rg.mux.setLegStandby(2, true)
		rg.mux.setLegStandby(3, true)
		rotateWith(rg, mts[1], RotationAction{PromoteFromStandby: []int{2}})
		require.True(t, legParked(t, rg, mts[3]), "the leg the policy did not name must stay parked")
		require.False(t, legParked(t, rg, mts[2]), "the leg the policy named must be promoted")
	})
	t.Run("drop", func(t *testing.T) {
		rg, mts, _ := createMuxRouteGroup(t, 4)
		rotateWith(rg, mts[1], RotationAction{DropLegs: []int{2}})
		require.GreaterOrEqual(t, legIdxOf(rg, mts[3]), 0, "the leg the policy did not name must stay")
		require.Equal(t, -1, legIdxOf(rg, mts[2]), "the leg the policy named must be dropped")
	})
}

// TestReelectPrimaryMovesTheChosenLeg removes a leg between the band's choice
// of a new primary and the swap.
func TestReelectPrimaryMovesTheChosenLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)
	rg.muxEvents = &muxEventRing{}
	chosen := mts[2].Entry.ID
	rg.pruneDeadLegs([]uuid.UUID{mts[1].Entry.ID}, "test", "test")

	rg.reelectPrimary(chosen)

	require.Equal(t, 0, legIdxOf(rg, mts[2]), "the chosen leg must take the primary slot")
}

// TestRefutedParkUndoneOnItsOwnLeg removes a leg between a park trial's verdict
// and the unpark.
func TestRefutedParkUndoneOnItsOwnLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)
	rg.muxEvents = &muxEventRing{}
	rg.mux.setLegStandby(2, true)
	rg.mu.Lock()
	tps := legTransports(rg.legs)
	rg.mu.Unlock()
	v := sbdTrialVerdict{tpID: mts[2].Entry.ID, keeper: mts[0].Entry.ID, leg: 2, failed: true}
	rg.pruneDeadLegs([]uuid.UUID{mts[1].Entry.ID}, "test", "test")

	rg.undoRefutedPark(v, tps, "test", "test")

	require.False(t, legParked(t, rg, mts[2]), "the refuted park must be undone on its own leg")
}

// TestLegStateResyncCoversEveryLeg removes a leg while the resync is writing.
// Every remaining aux leg must still be re-asserted in that pass.
func TestLegStateResyncCoversEveryLeg(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 4)
	rg.mux.legStateEnabled = true
	pruneOnLegState(rg, conns[1], mts[1])
	second, third := countLegState(conns[2]), countLegState(conns[3])

	rg.legStateResyncServiceFn(0)

	require.Equal(t, 1, *third)
	require.Equal(t, 1, *second, "the leg that moved down one index must not be skipped")
}

// splitHost is a legSplitHost with nothing registered.
type splitHost struct{}

func (splitHost) rehomeGroupFor(routing.RouteDescriptor) *RouteGroup           { return nil }
func (splitHost) splitGroupPort(routing.RouteDescriptor) (routing.Port, error) { return 50000, nil }
func (splitHost) registerSplitGroup(*RouteGroup) error                         { return nil }

// TestFailedSplitUnparksItsOwnLeg removes a leg while the split request is
// being written and fails the write. The quiesced leg must be active again.
func TestFailedSplitUnparksItsOwnLeg(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 4)
	rg.mux.legRehomeEnabled = true
	rg.rehomeHost = splitHost{}
	var once sync.Once
	conns[2].onWrite = func([]byte) {
		once.Do(func() { rg.pruneDeadLegs([]uuid.UUID{mts[1].Entry.ID}, "test", "test") })
	}
	conns[2].writeErr = errors.New("test: write failed")

	_, err := rg.splitLeg(mts[2].Entry.ID, "test")

	require.Error(t, err)
	require.False(t, legParked(t, rg, mts[2]), "a failed split must leave its leg active")
	require.False(t, legParked(t, rg, mts[3]))
}
