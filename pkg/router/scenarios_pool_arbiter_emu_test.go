// Package router pkg/router/scenarios_pool_arbiter_emu_test.go c2-net-routing
//
// The standby-pool ARBITER on the emulated testbed: one ACTIVE tunnel at mux
// width 2 and six STANDBY tunnels to the same exit, all sharing one routing
// table per end so a chain that changes hands is still followed by the data
// plane.
//
// Two claims, which are the two halves of pool_arbiter.go:
//
//   - a standby tunnel stays SINGLE-LEG whatever the width says (the rig run
//     that grew 64 legs to one exit is what this gate is for), and
//   - an ACTIVE tunnel under upload load takes ONE leg from that pool and
//     gives it back when the load is gone.
package router

import (
	"fmt"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// poolArbiterRig builds one active group and n standby groups between the same
// two visors, every group over its own emulated leg.
func poolArbiterRig(t *testing.T, n int) (g *emuRig, pool []*emuRig) {
	t.Helper()
	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.PanicLevel)
	shared := newEmuShared(ml)
	g = newEmuRig(t, emuOpts{
		Legs:      []emuLegSpec{symmetric("active-2MBs-120ms", 2*emuMB, 120*time.Millisecond, 2*emuMB)},
		LegRehome: true, SrcPort: 1, DstPort: 2, RidBase: 0, Shared: shared,
	})
	for i := 0; i < n; i++ {
		// Distinct latencies so the arbiter's ranking has something to rank:
		// the FIRST pooled tunnel is the fastest and must be the one taken.
		rtt := time.Duration(130+10*i) * time.Millisecond
		s := newEmuRig(t, emuOpts{
			Legs:      []emuLegSpec{symmetric(fmt.Sprintf("pool%d-2MBs-%v", i, rtt), 2*emuMB, rtt, 2*emuMB)},
			LegRehome: true,
			SrcPort:   routing.Port(3 + 2*i), DstPort: routing.Port(4 + 2*i),
			RidBase: 100 * (i + 1), Shared: shared,
		})
		pool = append(pool, s)
	}
	return g, pool
}

// TestEmuPoolArbiterTakesOneLegAndKeepsTheStandbyTunnelsSingleLeg is the gate.
func TestEmuPoolArbiterTakesOneLegAndKeepsTheStandbyTunnelsSingleLeg(t *testing.T) {
	const width = 2
	g, pool := poolArbiterRig(t, 6)

	// The app's labels: one active tunnel, six standby. This is the only thing
	// the router knows about the pool, and the whole arbiter turns on it.
	g.A.rg.SetTunnelRole(tunnelRoleActive)
	standby := make([]*RouteGroup, 0, len(pool))
	widened := 0
	for _, s := range pool {
		s.A.rg.SetTunnelRole(tunnelRoleStandby)
		// Width 2 offered to a STANDBY tunnel, exactly as the visor-wide mux
		// width offers it today. It must be refused: clamped to one leg, and
		// the self-heal top-up must never call its add.
		s.A.rg.SetSelfHeal(func([]string) { widened++ }, width)
		require.Equal(t, 1, s.A.rg.muxWidthTarget(),
			"a standby tunnel's width must be clamped to a single leg")
		s.A.rg.maybeSelfHeal()
		standby = append(standby, s.A.rg)
	}
	// The ACTIVE tunnel gets the same width and keeps it.
	g.A.rg.SetSelfHeal(nil, width)
	require.Equal(t, width, g.A.rg.muxWidthTarget(), "an active tunnel keeps the mux width")

	// Load: a real upload over the active group, with the arbiter ticking
	// against it the way the router's own loop does.
	done := make(chan *emuTransfer, 1)
	go func() { done <- g.Transfer(emuUp, 8*emuMB, emuTimeout) }()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		poolArbiterStep(g.A.rg, standby, time.Now(), nil)
		if a, _ := g.AddedLegs(); a >= width {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	ga, gb := g.AddedLegs()
	require.Equal(t, width, ga, "the loaded active tunnel must have taken one leg from the pool")
	require.Equal(t, width, gb, "the exit's side of the active group must hold the taken chain")

	x := <-done
	require.True(t, x.HashOK, "the upload that drove the arbiter must still arrive intact: got %d/%d", x.Got, x.Bytes)

	// Exactly one tunnel was spent, it was the FASTEST one, and every other
	// pooled tunnel is untouched and still single-leg.
	spent := 0
	for i, s := range pool {
		a, b := s.AddedLegs()
		if s.A.rg.isClosed() {
			spent++
			require.Equal(t, 0, i, "the arbiter must take the lowest-latency pooled tunnel first")
			continue
		}
		require.Equal(t, 1, a, "pooled tunnel %d must stay single-leg", i)
		require.Equal(t, 1, b, "pooled tunnel %d must stay single-leg at the exit", i)
	}
	require.Equal(t, 1, spent, "at most one leg per pool.leg_interval")
	require.Zero(t, widened, "the self-heal top-up must never widen a standby tunnel")
	require.True(t, rehomeEventSeen(g.A.rg, MuxEventPoolLegTaken, "took standby tunnel"),
		"the take must be recorded as pool_leg_taken with its source port")

	// RELEASE: no load, past pool.leg_release. The future clock is the seam —
	// the same comparison the router's loop makes, without a 30 s wait.
	// The forward fan-out latch expires on the REAL clock, so the first ticks
	// still read as loaded; the synthetic clock advances past pool.leg_release
	// once the latch has dropped, which is exactly the router loop's sequence.
	future := time.Now()
	for i := 0; i < 40; i++ {
		future = future.Add(10 * time.Second)
		poolArbiterStep(g.A.rg, standby, future, nil)
		if a, _ := g.AddedLegs(); a == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	ga, _ = g.AddedLegs()
	require.Equal(t, 1, ga, "an idle active tunnel must release the leg it took from the pool")
	require.True(t, rehomeEventSeen(g.A.rg, MuxEventPoolLegReleased, "released back to the pool"),
		"the release must be recorded as pool_leg_released")
}
