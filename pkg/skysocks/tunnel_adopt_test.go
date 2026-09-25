// Package skysocks pkg/skysocks/tunnel_adopt_test.go
package skysocks

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// fakeAdopt is the app's adopting dial against an in-process yamux server.
type fakeAdopt struct {
	mu    sync.Mutex
	ports []uint16
	fail  bool
}

func (f *fakeAdopt) dial(t *testing.T) func(uint16) (net.Conn, error) {
	return func(port uint16) (net.Conn, error) {
		f.mu.Lock()
		f.ports = append(f.ports, port)
		fail := f.fail
		f.mu.Unlock()
		if fail {
			return nil, errors.New("exit refused the adoption")
		}
		a, b := net.Pipe()
		srv, err := yamux.Server(b, yamux.DefaultConfig())
		require.NoError(t, err)
		t.Cleanup(func() { _ = srv.Close(); _ = a.Close(); _ = b.Close() }) //nolint:errcheck
		return a, nil
	}
}

func (f *fakeAdopt) calls() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.ports...)
}

// With no own standby, a wider target is met by ADOPTING the reserve.
func TestReconcileAdoptsALegReserveWhenThePoolIsEmpty(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 0)
	f := &fakeAdopt{}
	c.SetReserveAdopt(f.dial(t))
	c.noteReserves([]legReserve{{port: 40001, rttMs: 80, rttOK: true}})

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 2}))
	c.reconcileLiveKnobs()
	require.Eventually(t, func() bool { return c.activeLiveCount() == 2 }, 5*time.Second, 10*time.Millisecond,
		"the adopted reserve joins the active set")
	require.Equal(t, []uint16{40001}, f.calls(), "the dial names the reserve by its far-end port")
	require.Eventually(t, func() bool { return !c.adoptInFlight.Load() }, time.Second, 10*time.Millisecond)
}

// A reserve that ranks WORSE than the own standby leaves the promotion to the
// standby, and nothing is dialed.
func TestReconcilePrefersABetterOwnStandby(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 1) // its standby has a 100 ms RTT
	for s := range c.standby {
		c.recvStamp[s].stamp.Store(time.Now().UnixNano()) // ...and a fresh one
	}
	f := &fakeAdopt{}
	c.SetReserveAdopt(f.dial(t))
	c.noteReserves([]legReserve{{port: 40001, rttMs: 400, rttOK: true}})

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 2}))
	c.reconcileLiveKnobs()
	require.Equal(t, 2, c.activeLiveCount())
	require.Empty(t, f.calls(), "no adoption when the own standby ranks better")
}

// A reserve at least as good as the own standby is adopted; if the exit
// refuses, the own standby is promoted instead.
func TestReconcileFallsBackToTheStandbyWhenAdoptionFails(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 1)
	f := &fakeAdopt{fail: true}
	c.SetReserveAdopt(f.dial(t))
	c.noteReserves([]legReserve{{port: 40001, rttMs: 20, rttOK: true}})

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 2}))
	c.reconcileLiveKnobs()
	require.Eventually(t, func() bool { return c.activeLiveCount() == 2 }, 5*time.Second, 10*time.Millisecond,
		"the refused adoption falls back to the own standby")
	require.Equal(t, []uint16{40001}, f.calls())
	require.Equal(t, 2, c.liveSessionCount(), "nothing new was added")
}

// tunnel.adopt_reserves=false restores the old behavior.
func TestAdoptReservesKnobOff(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	require.True(t, tunnelAdoptReserves(), "on by default")
	c := liveOpsClient(t, 0)
	f := &fakeAdopt{}
	c.SetReserveAdopt(f.dial(t))
	c.noteReserves([]legReserve{{port: 40001}})

	// One Apply: it installs values wholesale, so a second would reset the first.
	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelAdoptReserves: 0, skysettings.TunnelCount: 2}))
	c.reconcileLiveKnobs()
	require.Equal(t, 1, c.activeLiveCount())
	require.Never(t, func() bool { return len(f.calls()) > 0 }, 200*time.Millisecond, 20*time.Millisecond)
}

// Only live leg reserves with a far-end port are adoptable, best RTT first.
func TestReservesFromSnapshot(t *testing.T) {
	snap := proxystatus.Snapshot{Tunnels: []proxystatus.Tunnel{
		{Role: "active", RemotePort: 3},
		{LegReserve: true, RemotePort: 40001, Legs: []proxystatus.Leg{{Alive: true, RouteLatencyMS: 90}}},
		{LegReserve: true, RemotePort: 40002, Legs: []proxystatus.Leg{{Alive: false, RouteLatencyMS: 10}}},
		{LegReserve: true, RemotePort: 40003, Legs: []proxystatus.Leg{{Alive: true, RouteLatencyMS: 30}}},
		{LegReserve: true, RemotePort: 40004, Legs: []proxystatus.Leg{{Alive: true}}},
		{LegReserve: true},
	}}
	got := reservesFrom(snap)
	ports := make([]uint16, 0, len(got))
	for _, r := range got {
		ports = append(ports, r.port)
	}
	require.Equal(t, []uint16{40003, 40001, 40004}, ports)
}

// A refused adoption is a property of the EXIT: no other reserve is tried for
// tunnel.adopt_refused_hold, and the slot is filled by a dial the moment the
// adoption fails when there is no own standby to promote.
func TestAdoptRefusalHoldsFurtherAdoptions(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 0)
	f := &fakeAdopt{fail: true}
	c.SetReserveAdopt(f.dial(t))
	redial := (&fakeAdopt{}).dial(t)
	c.SetTunnelRedial(func() (net.Conn, error) { return redial(0) })
	c.noteReserves([]legReserve{{port: 40001}, {port: 40002}})

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 2}))
	c.reconcileLiveKnobs()
	require.Eventually(t, func() bool { return c.activeLiveCount() == 2 }, 5*time.Second, 10*time.Millisecond,
		"the refused adoption falls through to a dial")
	require.Equal(t, []uint16{40001}, f.calls(), "the dial replaced the adoption, it did not try the next reserve")

	// A fresh snapshot shows reserves again; the hold keeps them untried.
	c.noteReserves([]legReserve{{port: 40002}, {port: 40003}})
	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 3}))
	c.reconcileLiveKnobs()
	require.Never(t, func() bool { return len(f.calls()) > 1 }, 200*time.Millisecond, 20*time.Millisecond,
		"no adoption to a refusing exit within tunnel.adopt_refused_hold")

	// Once the hold is over, adoption is tried again.
	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelAdoptRefusedHold: int64(time.Millisecond), skysettings.TunnelCount: 3}))
	c.redialMu.Lock()
	c.adoptHeldUntil = time.Now()
	c.redialMu.Unlock()
	c.reconcileLiveKnobs()
	require.Eventually(t, func() bool { return len(f.calls()) == 2 }, 5*time.Second, 10*time.Millisecond)
}

// Under the hold a short active set is filled from the own pool IN THE SAME
// reconcile, with no adoption attempted.
func TestAdoptHoldPromotesInTheSameTick(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := liveOpsClient(t, 2)
	f := &fakeAdopt{fail: true}
	c.SetReserveAdopt(f.dial(t))
	c.noteReserves([]legReserve{{port: 40001, rttMs: 1, rttOK: true}})
	c.holdAdoption(40009, errors.New("peer refused to adopt the leg reserve"))

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 3}))
	c.reconcileLiveKnobs()
	require.Equal(t, 3, c.activeLiveCount(), "both standbys promoted in the one reconcile")
	require.Empty(t, f.calls())
}
