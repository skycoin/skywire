package skysocks

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// noteRecorder collects the tunnel events a Client reports to the visor, in
// order, the way the router's ring would see them.
type noteRecorder struct {
	mu   sync.Mutex
	got  []tunnelNote
	done chan struct{}
	want int
}

func newNoteRecorder(want int) *noteRecorder {
	return &noteRecorder{done: make(chan struct{}), want: want}
}

func (n *noteRecorder) note(port routing.Port, event, reason, role string) error {
	n.mu.Lock()
	n.got = append(n.got, tunnelNote{port: port, event: event, reason: reason, role: role})
	if len(n.got) == n.want {
		close(n.done)
	}
	n.mu.Unlock()
	return nil
}

func (n *noteRecorder) events() []tunnelNote {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]tunnelNote(nil), n.got...)
}

// failoverActiveRTT is the sole active tunnel's measured RTT in these tests.
// Every standby's RTT is given relative to it, and nothing here turns on the
// active tunnel's own number, so it is a constant rather than a parameter.
const failoverActiveRTT = 30

// failoverClient builds a Client with one active tunnel and len(standbyRTT)
// standby tunnels, each with the given RTT in ms and a distinct local port, so
// a promote can be attributed. Ports are 1000+i in session order.
func failoverClient(t *testing.T, standbyRTT ...float64) (*Client, []*yamux.Session, func()) {
	t.Helper()
	c := &Client{closeC: make(chan struct{}), streams: map[uint32]streamMeta{}}
	c.recvStamp = map[*yamux.Session]*tunnelMeter{}
	c.standby = map[*yamux.Session]bool{}
	closers := make([]func(), 0, 1+len(standbyRTT))
	sessions := make([]*yamux.Session, 0, 1+len(standbyRTT))

	add := func(rttMs float64, standby bool) {
		s, closeS := newTestSession(t)
		closers = append(closers, closeS)
		m := new(tunnelMeter)
		m.rttMs = rttMs
		m.port = routing.Port(1000 + len(sessions)) //nolint:gosec // G115: small test port
		m.stamp.Store(time.Now().UnixNano())
		c.sessions = append(c.sessions, s)
		c.recvStamp[s] = m
		if standby {
			c.standby[s] = true
		}
		sessions = append(sessions, s)
	}
	add(failoverActiveRTT, false)
	for _, rtt := range standbyRTT {
		add(rtt, true)
	}
	c.SetTunnelTarget(1)
	return c, sessions, func() {
		for _, fn := range closers {
			fn()
		}
	}
}

// The point of the pool: when an ACTIVE tunnel dies the best standby takes its
// place before the tick that noticed the death ends. Before this, the active
// width came back only from a fresh route setup — measured at 35-40 s ttfb
// after a first-hop cut.
func TestRetireTunnel_PromotesTheBestStandbyInTheSameTick(t *testing.T) {
	c, s, cleanup := failoverClient(t, 200, 40)
	defer cleanup()
	active, slow, fast := s[0], s[1], s[2]

	require.True(t, c.retireTunnel(active, "liveness: no pong and no bytes for 47s"))

	// Synchronously, with no waiting: the promote is part of the retire.
	require.False(t, c.IsStandby(fast), "the lowest-RTT standby takes the active slot")
	require.True(t, c.IsStandby(slow), "only ONE tunnel is promoted per death")
	require.Equal(t, 1, c.activeLiveCount())
	require.Same(t, fast, c.pickSessionFor(pickAny), "and the next stream lands on it at once")
}

// A STANDBY tunnel dying is not a failover: nothing was carrying streams, so
// nothing is promoted. Only the pool fill answers it.
func TestRetireTunnel_StandbyDeathPromotesNothing(t *testing.T) {
	c, s, cleanup := failoverClient(t, 200, 40)
	defer cleanup()

	require.True(t, c.retireTunnel(s[1], "tunnel session closed"))
	require.True(t, c.IsStandby(s[2]), "the surviving standby stays in the pool")
	require.Equal(t, 1, c.activeLiveCount())
}

// A closed session is never pruned from the session slice, so the keepalive
// loop sees a dead tunnel again on every tick. Promoting per sighting would
// drain the pool over one death.
func TestRetireTunnel_IsOnceOnly(t *testing.T) {
	c, s, cleanup := failoverClient(t, 200, 40)
	defer cleanup()

	require.True(t, c.retireTunnel(s[0], "first sighting"))
	for i := 0; i < 5; i++ {
		require.False(t, c.retireTunnel(s[0], "same tunnel, later tick"))
	}
	require.True(t, c.IsStandby(s[1]), "exactly one standby was spent")
	require.Equal(t, 1, c.activeLiveCount())
}

// Criterion 6, "no rebuild of the surviving groups": with the active set made
// whole by a promote, the re-dial stands down. The replacement route is dialed
// into the pool TAIL in the background instead.
func TestMaybeRedial_StandsDownAfterAFailoverPromote(t *testing.T) {
	c, s, cleanup := failoverClient(t, 40)
	defer cleanup()

	var redials atomic.Int64
	c.SetTunnelRedial(func() (net.Conn, error) {
		redials.Add(1)
		conn, closeConn := newTestTunnelConn(t)
		t.Cleanup(closeConn)
		return conn, nil
	})

	require.True(t, c.retireTunnel(s[0], "liveness"))
	c.maybeRedial(c.liveSessionCount())
	require.Eventually(t, func() bool { return !c.redialInFlight.Load() }, time.Second, 5*time.Millisecond)
	require.Zero(t, redials.Load(), "the promoted standby IS the replacement; rebuilding a route group on top of it is the churn criterion 6 forbids")
}

// ...and the converse: with an empty pool there is nothing to promote, so the
// re-dial is still the answer and still adds an ACTIVE tunnel.
func TestMaybeRedial_StillFiresWhenThePoolIsEmpty(t *testing.T) {
	c, s, cleanup := failoverClient(t)
	defer cleanup()
	// Two active tunnels, one of which is about to die, so the client is not in
	// total collapse (which the app's --reconnect owns, not the re-dial).
	extra, closeExtra := newTestTunnelConn(t)
	defer closeExtra()
	require.NoError(t, c.AddTunnel(extra))
	c.SetTunnelTarget(2)

	var redials atomic.Int64
	c.SetTunnelRedial(func() (net.Conn, error) {
		redials.Add(1)
		conn, closeConn := newTestTunnelConn(t)
		t.Cleanup(closeConn)
		return conn, nil
	})

	require.True(t, c.retireTunnel(s[0], "liveness"))
	require.Equal(t, 1, c.activeLiveCount())
	c.maybeRedial(c.liveSessionCount())
	require.Eventually(t, func() bool { return redials.Load() == 1 }, time.Second, 5*time.Millisecond)
}

// The switch has to be readable afterwards, or it did not happen as far as a
// bench is concerned: a retire and a promote reach the visor in that order,
// each naming its tunnel by the local port it was dialed from, and the promote
// carries the role that re-labels the route group.
func TestTunnelEvents_ReachTheVisorInOrderWithRoles(t *testing.T) {
	c, s, cleanup := failoverClient(t, 40)
	defer cleanup()
	rec := newNoteRecorder(2)
	c.muxNote = rec.note
	c.startMuxNotes()
	defer close(c.closeC)

	require.True(t, c.retireTunnel(s[0], "liveness: no pong and no bytes for 47s"))

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("events did not reach the visor: %+v", rec.events())
	}
	got := rec.events()
	require.Equal(t, router.MuxEventTunnelRetired, got[0].event)
	require.EqualValues(t, 1000, got[0].port, "the dead tunnel is named by its own port")
	require.Equal(t, "liveness: no pong and no bytes for 47s", got[0].reason)
	require.Empty(t, got[0].role, "a retire says a tunnel is gone, not what it became")

	require.Equal(t, router.MuxEventTunnelPromoted, got[1].event)
	require.EqualValues(t, 1001, got[1].port, "the promoted tunnel is named by ITS port")
	require.Equal(t, "failover: active tunnel died", got[1].reason)
	require.Equal(t, TunnelRoleActive, got[1].role, "so `visor state --select mux_route_groups` is right after the switch")
}

// Parking is the promote's counterpart, so churn is countable in both
// directions, and it re-labels the group too.
func TestParkTunnel_MarksStandbyAndReportsIt(t *testing.T) {
	c, s, cleanup := failoverClient(t, 40)
	defer cleanup()
	rec := newNoteRecorder(1)
	c.muxNote = rec.note
	c.startMuxNotes()
	defer close(c.closeC)

	require.True(t, c.parkTunnel(s[0], "promoter: standby beat active by 1.3x for 15 s"))
	require.True(t, c.IsStandby(s[0]))
	require.False(t, c.parkTunnel(s[0], "already parked"), "parking a standby tunnel is a no-op")

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the park never reached the visor")
	}
	got := rec.events()
	require.Len(t, got, 1, "the second park reported nothing")
	require.Equal(t, router.MuxEventTunnelParked, got[0].event)
	require.Equal(t, TunnelRoleStandby, got[0].role)
}

// A tunnel that has stopped answering must not be promoted over one that is
// answering, however good its last recorded RTT was — this is the "silently
// black-holing standby promoted into a transfer" case. It stays ELIGIBLE, so a
// pool of nothing but stale tunnels is still better than a route setup.
func TestPromoteBestStandby_PrefersAFreshTunnelOverAStaleFasterOne(t *testing.T) {
	c, s, cleanup := failoverClient(t, 5, 80)
	defer cleanup()
	stale, fresh := s[1], s[2]
	c.recvStamp[stale].stamp.Store(time.Now().Add(-10 * standbyRTTStale).UnixNano())

	require.Same(t, fresh, c.promoteBestStandby("failover: active tunnel died"))
	require.True(t, c.IsStandby(stale))

	// With only the stale one left, it is promoted anyway.
	require.True(t, c.retireTunnel(fresh, "liveness"))
	require.False(t, c.IsStandby(stale), "a stale standby still beats an 8-9 s route setup")
}

// The measured sequence, end to end. On the rig 2026-09-17 (campaign21,
// develop 67dddb8be) the bench cut the ACTIVE tunnel's first hop with six
// healthy standbys held. What happened: ttfb 4.45 s, the cut group was not
// replaced from the pool at all, the refill dial took the next-ranked
// candidate — a sudph hop to a fleet peer — and that brand-new group JOINED
// the active set, so the three 50 MB uploads that followed ran at
// 0.45-0.52 MB/s (0.05-0.15 of the paired reference) while six measured stcpr
// tunnels sat idle, and one group was rebuilt.
//
// What must happen instead: the active death is answered from the pool in the
// same tick, and the refill dial lands in STANDBY — never in the active set.
func TestFailover_ActiveDiesStandbyPromotedRefillLandsStandby(t *testing.T) {
	c, s, cleanup := failoverClient(t, 60, 45)
	defer cleanup()
	active, held1, held2 := s[0], s[1], s[2]

	var closers []func()
	defer func() {
		for _, fn := range closers {
			fn()
		}
	}()
	c.SetStandbyPool(4)
	c.SetPoolDial(func() (net.Conn, error) {
		conn, closeConn := newTestTunnelConn(t)
		closers = append(closers, closeConn)
		return conn, nil
	})

	// 1. the active tunnel dies, and a HELD tunnel takes the slot at once.
	require.True(t, c.retireTunnel(active, "liveness: no pong and no bytes for 47s"))
	require.False(t, c.IsStandby(held2), "the lower-RTT held tunnel is the replacement")
	require.True(t, c.IsStandby(held1))
	require.Equal(t, 1, c.activeLiveCount(), "the active width is whole again with no dial at all")

	// 2. the death re-armed the fill; the replacement route lands in the POOL.
	c.armPoolFill()
	c.maybePoolFill()
	waitFill(t, c)
	held, standby, _, _, _ := c.StandbyPoolState()
	require.Equal(t, 3, held)
	require.Equal(t, 2, standby, "a fresh dial joins the pool, never the active set")
	require.Equal(t, 1, c.activeLiveCount())

	// 3. and the active tunnel is still the one that was MEASURED, so a lone
	// stream does not ride the newest route on the strength of one ping.
	require.Same(t, held2, c.pickSessionFor(pickAny))
}

// An empty pool promotes nothing and says so, rather than picking a closed or
// an already-active session.
func TestPromoteBestStandby_EmptyPool(t *testing.T) {
	c, _, cleanup := failoverClient(t)
	defer cleanup()
	require.Nil(t, c.promoteBestStandby("failover: active tunnel died"))
	require.Equal(t, 1, c.activeLiveCount())
}
