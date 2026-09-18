// Package skysocks pkg/skysocks/client_live_ops_test.go
package skysocks

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// liveOpsClient builds a client holding one active tunnel and n standby ones,
// each with an RTT so the promoter and the shrink have something to rank by.
func liveOpsClient(t *testing.T, standbys int) *Client {
	t.Helper()
	active, closeA := newTestSession(t)
	t.Cleanup(closeA)

	c := &Client{
		closeC:    make(chan struct{}),
		streams:   map[uint32]streamMeta{},
		sessions:  []*yamux.Session{active},
		recvStamp: map[*yamux.Session]*tunnelMeter{active: {rttMs: 50, port: 49170}},
		standby:   map[*yamux.Session]bool{},
	}
	for i := 0; i < standbys; i++ {
		s, closeS := newTestSession(t)
		t.Cleanup(closeS)
		c.sessions = append(c.sessions, s)
		// Each standby is worse than the last, so the shrink's choice is
		// unambiguous and the promoter's is the first of them.
		c.recvStamp[s] = &tunnelMeter{rttMs: float64(100 + 10*i), port: routing.Port(49171 + i)} //nolint:gosec
		c.standby[s] = true
	}
	c.SetTunnelTarget(1)
	c.SetStandbyPool(1 + standbys)
	return c
}

// tunnel.count is a live shape: raising it promotes from the pool and lowering
// it parks the worst idle active tunnel, both without a restart.
func TestReconcileActiveSetFollowsTunnelCount(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 2)
	require.Equal(t, 1, c.activeLiveCount())

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 3}))
	c.reconcileLiveKnobs()
	require.Equal(t, 3, c.activeLiveCount(), "the pool filled the widened active set")

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 1}))
	c.reconcileLiveKnobs()
	require.Equal(t, 1, c.activeLiveCount(), "the extra tunnels were parked, not closed")
	require.Equal(t, 3, c.liveSessionCount(), "a shrink of the ACTIVE set gives up no tunnel")
}

// pool.size is the other live shape: lowered, the pool gives up its worst
// STANDBY tunnels on the next fill tick and never an active one.
func TestPoolShrinkFollowsPoolSize(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 3)
	require.Equal(t, 4, c.liveSessionCount())

	require.True(t, skysettings.Apply(map[string]int64{skysettings.PoolSize: 2}))
	c.reconcileLiveKnobs()
	c.maybePoolShrink()

	require.Equal(t, 2, c.liveSessionCount(), "held down to the new ceiling")
	require.Equal(t, 1, c.activeLiveCount(), "the active tunnel is never given up to a ceiling")
}

// pool.freeze holds the set still: no shrink, no fill, no promoter swap. A
// reconcile the operator asked for still runs, and so does the failover.
func TestPoolFreezeHoldsTheSetStill(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 3)
	dials := 0
	c.SetPoolDial(func() (net.Conn, error) { dials++; return nil, errors.New("no dial expected while frozen") })

	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.PoolFreeze: 1,
		skysettings.PoolSize:   1,
	}))
	c.reconcileLiveKnobs()
	c.maybePoolShrink()
	c.maybePoolFill()
	c.maybePromote()

	require.Equal(t, 4, c.liveSessionCount(), "frozen: nothing was given up")
	require.Zero(t, dials, "frozen: nothing was dialed")

	// Cleared, the same ceiling is enforced on the next tick.
	require.True(t, skysettings.Apply(map[string]int64{skysettings.PoolSize: 1}))
	c.maybePoolShrink()
	require.Equal(t, 1, c.liveSessionCount())
}

// A cut closes exactly the tunnel the operator named, by the port `mux info`
// prints, and leaves every other tunnel up.
func TestCutTunnelClosesOnlyThatTunnel(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 2)
	require.Equal(t, 3, c.liveSessionCount())

	require.False(t, c.cutTunnel(0), "a zero port names nothing")
	require.False(t, c.cutTunnel(51000), "an unknown port names nothing")
	require.Equal(t, 3, c.liveSessionCount())

	require.True(t, c.cutTunnel(49172))
	require.Equal(t, 2, c.liveSessionCount())
	require.Equal(t, 1, c.activeLiveCount(), "cutting a standby leaves the active set alone")

	// And the active tunnel's own cut is a failover: the slot is refilled from
	// standby in the same call.
	require.True(t, c.cutTunnel(49170))
	require.Equal(t, 1, c.liveSessionCount())
	require.Equal(t, 1, c.activeLiveCount())
}

// An op is applied once and acked by sequence, whatever the settings version
// does — the two halves of the pull are independent.
func TestApplyOpsCutsOnceAndAcks(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 1)

	ops := []appserver.AppOp{{Seq: 1, Kind: appserver.AppOpCutTunnel, Arg: 49171}}
	c.applyOps(ops)
	require.EqualValues(t, 1, c.opsApplied)
	require.Equal(t, 1, c.liveSessionCount())

	// The same op carried again (an answer the visor had not seen acked) does
	// nothing a second time.
	c.applyOps(ops)
	require.Equal(t, 1, c.liveSessionCount())

	// An unknown op is acked rather than retried forever.
	c.applyOps([]appserver.AppOp{{Seq: 2, Kind: "from-a-newer-visor"}})
	require.EqualValues(t, 2, c.opsApplied)
}

// The pool's candidate filter is read live: what the dial closure is handed is
// whatever the knobs say at that instant.
func TestPoolFilterIsLive(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 0)

	excl, types := c.PoolFilter()
	require.Empty(t, excl)
	require.Empty(t, types)

	const pk = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
	require.True(t, skysettings.ApplyText(map[string]string{
		skysettings.PoolExcludePKs:     pk,
		skysettings.PoolRequireTpTypes: "stcpr,sudph",
	}))
	excl, types = c.PoolFilter()
	require.Equal(t, []string{pk}, excl)
	require.Equal(t, []string{"stcpr", "sudph"}, types)
}

// The knobs are read on a tick, so a client with no RTT history still answers
// promptly — this only guards the helpers above against a hang.
func TestLiveOpsTerminate(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 1)
	done := make(chan struct{})
	go func() {
		c.reconcileActiveSet("test")
		c.maybePoolShrink()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile did not terminate")
	}
}
