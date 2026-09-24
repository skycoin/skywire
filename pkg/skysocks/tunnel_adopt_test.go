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
	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelAdoptReserves: 0}))
	c := liveOpsClient(t, 0)
	f := &fakeAdopt{}
	c.SetReserveAdopt(f.dial(t))
	c.noteReserves([]legReserve{{port: 40001}})

	require.True(t, skysettings.Apply(map[string]int64{skysettings.TunnelCount: 2}))
	c.reconcileLiveKnobs()
	require.Equal(t, 1, c.activeLiveCount())
	require.Empty(t, f.calls())
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
