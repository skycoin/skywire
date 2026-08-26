// Package skysocks multi-tunnel re-dial (health-management) tests.
package skysocks

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
)

// newRedialConn returns a fresh net.Conn whose peer runs a yamux server draining
// accepted streams, so AddTunnel wraps it into a live (non-closed) session. The
// cleanup tears both ends down. This is what a re-dial callback hands back.
func newRedialConn(t *testing.T) (net.Conn, func()) {
	t.Helper()
	a, b := net.Pipe()
	go func() {
		ssess, e := yamux.Server(b, yamux.DefaultConfig())
		if e != nil {
			return
		}
		for {
			st, ae := ssess.Accept()
			if ae != nil {
				return
			}
			go io.Copy(io.Discard, st) //nolint:errcheck
		}
	}()
	return a, func() { _ = a.Close(); _ = b.Close() } //nolint:errcheck
}

// A Client at target N with one dead tunnel re-dials exactly one replacement,
// restoring the live count to N, and the in-flight guard prevents a second
// concurrent re-dial while the first is running.
func TestMaybeRedial_ReplacesDeadTunnelOnce(t *testing.T) {
	live, cleanupLive := newTestSession(t)
	defer cleanupLive()
	dead, cleanupDead := newTestSession(t)
	defer cleanupDead()
	require.NoError(t, dead.Close()) // one tunnel is dead → live count 1 < target 2

	c := &Client{
		sessions: []*yamux.Session{live, dead},
		closeC:   make(chan struct{}),
		target:   2,
	}

	// The re-dial callback blocks until released, so we can observe the
	// single-flight guard: a second maybeRedial while the first is in flight must
	// NOT invoke the callback again.
	var calls int32
	release := make(chan struct{})
	var replacement net.Conn
	var cleanupReplacement func()
	c.SetTunnelRedial(func() (net.Conn, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		replacement, cleanupReplacement = newRedialConn(t)
		return replacement, nil
	})

	// First trigger: live=1 < target=2 → one re-dial starts and blocks in fn.
	c.maybeRedial(1)
	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == 1 },
		time.Second, 5*time.Millisecond, "the dead tunnel triggers one re-dial")
	require.True(t, c.redialInFlight.Load(), "a re-dial is in flight")

	// Second trigger while the first is still in flight: the single-flight guard
	// must suppress it — no second callback invocation, still one tunnel added.
	c.maybeRedial(1)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "in-flight guard blocks a concurrent re-dial")

	// Let the first re-dial finish; AddTunnel restores the count to the target.
	close(release)
	require.Eventually(t, func() bool { return c.liveSessionCount() == 2 },
		time.Second, 5*time.Millisecond, "AddTunnel restores the live count to N")
	require.Len(t, c.snapshotSessions(), 3, "one live + one dead + the replacement")
	require.False(t, c.redialInFlight.Load(), "the in-flight guard clears after completion")
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "exactly one re-dial happened")
	if cleanupReplacement != nil {
		defer cleanupReplacement()
	}

	// At/above target no further re-dial fires.
	c.maybeRedial(c.liveSessionCount())
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "no re-dial when live >= target")
}

// N==1 (and any all-closed / no-callback state) never re-dials: a single tunnel's
// death is total collapse the app's --reconnect owns, not a per-tunnel re-dial.
func TestMaybeRedial_SingleTunnelAndGuards(t *testing.T) {
	var calls int32
	countingRedial := func() (net.Conn, error) {
		atomic.AddInt32(&calls, 1)
		conn, _ := newRedialConn(t)
		return conn, nil
	}

	// N==1: even with a callback wired, live>=target so no re-dial.
	s, cleanup := newTestSession(t)
	defer cleanup()
	c1 := &Client{sessions: []*yamux.Session{s}, closeC: make(chan struct{}), target: 1}
	c1.SetTunnelRedial(countingRedial)
	c1.maybeRedial(1)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls), "N==1 never re-dials")

	// All tunnels dead (live<=0): total collapse, not a re-dial trigger.
	c1.maybeRedial(0)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls), "all-closed is left to the app's reconnect")

	// No callback wired: no-op even below target.
	c2 := &Client{sessions: []*yamux.Session{s}, closeC: make(chan struct{}), target: 3}
	c2.maybeRedial(1)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls), "no re-dial without a callback")
}

// After maxRedialFails consecutive failures the loop backs off (stops re-dialing)
// until a fresh death re-arms it via resetRedialBackoff.
func TestMaybeRedial_BacksOffAfterFailuresUntilReset(t *testing.T) {
	live, cleanup := newTestSession(t)
	defer cleanup()
	c := &Client{sessions: []*yamux.Session{live}, closeC: make(chan struct{}), target: 3}

	var calls int32
	c.SetTunnelRedial(func() (net.Conn, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("exit unreachable")
	})

	// Each attempt fails and increments the counter; the in-flight guard clears
	// synchronously on failure, so serialized calls accumulate failures.
	for i := 0; i < maxRedialFails; i++ {
		c.maybeRedial(1)
		require.Eventually(t, func() bool { return !c.redialInFlight.Load() },
			time.Second, 5*time.Millisecond)
	}
	require.Equal(t, int32(maxRedialFails), atomic.LoadInt32(&calls))

	// Now backed off: further triggers are suppressed.
	c.maybeRedial(1)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(maxRedialFails), atomic.LoadInt32(&calls), "backed off after consecutive failures")

	// A fresh death re-arms it.
	c.resetRedialBackoff()
	c.maybeRedial(1)
	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == maxRedialFails+1 },
		time.Second, 5*time.Millisecond, "reset re-arms re-dial")
}
