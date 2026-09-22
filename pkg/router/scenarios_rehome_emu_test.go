// Package router pkg/router/scenarios_rehome_emu_test.go c2-net-routing
//
// The leg re-home gate, on the emulated testbed: two route groups between the
// SAME two ends — an active one and a standby one, exactly the shape the proxy's
// standby pool holds — and the standby's whole built chain moved into the active
// group as a second mux leg, in place, with no dial. The download over the
// active group must then aggregate over both chains.
//
// Both rigs share one routing table and one dispatcher per end (emuShared), so
// a frame reaches whichever group owns its consume rule at the moment it lands.
// That is the only harness property re-home needs and the reason it is here
// rather than in a unit test: the rewrite is only real if the data plane
// follows it.
package router

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// rehomeRigPair builds the active group G (ports 1/2) and the standby group S
// (ports 3/4) over their own emulated legs between the same two visors.
func rehomeRigPair(t *testing.T, gLeg, sLeg emuLegSpec, rehome bool) (g, s *emuRig) {
	t.Helper()
	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.PanicLevel)
	shared := newEmuShared(ml)
	g = newEmuRig(t, emuOpts{
		Legs: []emuLegSpec{gLeg}, LegRehome: rehome,
		SrcPort: 1, DstPort: 2, RidBase: 0, Shared: shared,
	})
	s = newEmuRig(t, emuOpts{
		Legs: []emuLegSpec{sLeg}, LegRehome: rehome,
		SrcPort: 3, DstPort: 4, RidBase: 100, Shared: shared,
	})
	return g, s
}

// TestEmuRehomeStandbyChainIntoActiveGroup is the gate: a standby tunnel's
// chain adopted by the active group must turn into real aggregation — the same
// download over the same group, at better than 1.5x the rate its single leg
// gives — and the standby group must be gone, not left half-alive.
func TestEmuRehomeStandbyChainIntoActiveGroup(t *testing.T) {
	const bytes = 8 * emuMB
	gLeg := symmetric("active-2MBs-120ms", 2*emuMB, 120*time.Millisecond, 2*emuMB)
	sLeg := symmetric("standby-2MBs-150ms", 2*emuMB, 150*time.Millisecond, 2*emuMB)

	g, s := rehomeRigPair(t, gLeg, sLeg, true)

	gaLegs, gbLegs := g.AddedLegs()
	require.Equal(t, 1, gaLegs, "the active group starts with one leg")
	require.Equal(t, 1, gbLegs)

	// PAIRED bar: the same download, over the same group, on the same run —
	// the active group's own leg before the adoption. A separate baseline rig
	// measures the runner's load as much as the scheduler; this does not.
	before := g.Transfer(emuDown, bytes, emuTimeout)
	require.True(t, before.HashOK, "the single-leg reference must complete: got %d/%d", before.Got, before.Bytes)
	beforeBps := float64(before.Got) / before.Elapsed.Seconds()

	require.NoError(t, rehomeChain(g.A.rg, s.A.rg), "re-home of the standby chain")

	gaLegs, gbLegs = g.AddedLegs()
	require.Equal(t, 2, gaLegs, "the initiator's active group must hold the adopted chain")
	require.Equal(t, 2, gbLegs, "the exit's active group must hold the adopted chain")

	// The standby group is spent: legless on both ends and closed.
	require.Eventually(t, func() bool {
		a, b := s.AddedLegs()
		return a == 0 && b == 0 && s.A.rg.isClosed() && s.B.rg.isClosed()
	}, 5*time.Second, 20*time.Millisecond, "the consumed standby group must close on both ends")

	// The adopted chain joins with no ECF delay basis and no proven goodput of
	// its own in THIS group, so the first transfer after an adoption pays the
	// same cold-start ramp a freshly dialed leg pays. Warm it, then measure —
	// the campaign's persistent-connection method, and the claim under test is
	// about the steady state, not the ramp.
	warm := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, warm.HashOK, "the warm-up over the re-homed pair must complete: got %d/%d", warm.Got, warm.Bytes)

	x := g.Transfer(emuDown, bytes, emuTimeout)
	require.NoError(t, x.Err)
	require.True(t, x.HashOK, "the download over the re-homed pair must arrive intact: got %d/%d", x.Got, x.Bytes)

	got := float64(x.Got) / x.Elapsed.Seconds()
	ratio := got / beforeBps
	t.Log(g.Summary("rehome-two-chains", emuDown, x).Table())
	t.Log(rehomeLegSplit(g.B.rg), " ", g.legBases(g.B))
	t.Logf("rehome: %.2f MB/s over two chains vs %.2f MB/s on the same group's own leg (x%.2f) in %s",
		got/emuMB, beforeBps/emuMB, ratio, x.Elapsed.Round(time.Millisecond))
	if ratio < 1.5 {
		t.Errorf("the adopted chain added %.2fx, under the 1.5x the second leg is worth", ratio)
	}

	// And it is recorded as an adoption, not as an ordinary dial.
	require.True(t, rehomeEventSeen(g.A.rg, MuxEventLegAdded, "rehome from :4"),
		"the adoption must name where the chain came from")
	require.True(t, rehomeEventSeen(s.A.rg, MuxEventTunnelConsumed, "mux leg"),
		"the standby tunnel must be recorded as consumed, not as a death")
}

// TestEmuRehomeRefusedWithoutTheCapability is the fallback: a peer that never
// negotiated CapLegRehome must leave BOTH groups exactly as they were, so the
// caller can spend the pooled tunnel the dial-based way instead.
func TestEmuRehomeRefusedWithoutTheCapability(t *testing.T) {
	gLeg := symmetric("active-2MBs-120ms", 2*emuMB, 120*time.Millisecond, 2*emuMB)
	sLeg := symmetric("standby-2MBs-150ms", 2*emuMB, 150*time.Millisecond, 2*emuMB)

	g, s := rehomeRigPair(t, gLeg, sLeg, false)

	require.ErrorIs(t, rehomeChain(g.A.rg, s.A.rg), ErrRehomeUnsupported)

	ga, gb := g.AddedLegs()
	sa, sb := s.AddedLegs()
	require.Equal(t, [4]int{1, 1, 1, 1}, [4]int{ga, gb, sa, sb}, "a refused re-home must move nothing")
	require.False(t, s.A.rg.isClosed(), "the standby group must survive a refusal")
	require.False(t, s.B.rg.isClosed())

	// Both groups still carry: the refusal cost neither of them anything.
	x := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, x.HashOK, "the active group must still carry after a refused re-home")
	y := s.Transfer(emuDown, emuMB, emuTimeout)
	require.True(t, y.HashOK, "the standby group must still carry after a refused re-home")
}

// rehomeLegSplit renders the SENDING side's per-leg share, including the leg
// that arrived by adoption (which the rig's leg-spec table cannot name).
func rehomeLegSplit(rg *RouteGroup) string {
	rg.mu.Lock()
	legs := rg.mux.snapshotLegs()
	rg.mu.Unlock()
	var b strings.Builder
	b.WriteString("split[")
	for i := range legs {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d: sent=%dKiB retx=%d standby=%v ready=%v",
			i, legs[i].SentBytes/1024, legs[i].Retransmits, rg.mux.isLegStandby(i), rg.mux.legReadyAt(i))
	}
	b.WriteString("]")
	return b.String()
}

// rehomeEventSeen reports whether the group's mux ring holds an event of the
// given kind whose reason mentions want.
func rehomeEventSeen(rg *RouteGroup, kind, want string) bool {
	for _, e := range rg.muxEvents.snapshot() {
		if e.Event == kind && strings.Contains(e.Reason, want) {
			return true
		}
	}
	return false
}
