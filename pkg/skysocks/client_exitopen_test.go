// Package skysocks exit-open (client→exit SOCKS5 method negotiation) timeout tests.
package skysocks

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"
)

// newAnsweringSession is newTestSession's peer with a working skysocks exit on
// the far end: it answers every stream's SOCKS5 greeting with 05 00, so
// openExit on it succeeds.
func newAnsweringSession(t *testing.T) (*yamux.Session, func()) {
	t.Helper()
	a, b := net.Pipe()
	ssess, err := yamux.Server(b, yamux.DefaultConfig())
	require.NoError(t, err)
	go func() {
		for {
			st, e := ssess.Accept()
			if e != nil {
				return
			}
			go func(st net.Conn) {
				hdr := make([]byte, 2)
				if _, e := io.ReadFull(st, hdr); e != nil {
					return
				}
				if _, e := io.ReadFull(st, make([]byte, int(hdr[1]))); e != nil {
					return
				}
				_, _ = st.Write([]byte{0x05, 0x00}) //nolint:errcheck
				_, _ = io.Copy(io.Discard, st)      //nolint:errcheck
			}(st)
		}
	}()
	csess, err := yamux.Client(a, yamux.DefaultConfig())
	require.NoError(t, err)
	return csess, func() {
		_ = csess.Close() //nolint:errcheck
		_ = ssess.Close() //nolint:errcheck
		_ = a.Close()     //nolint:errcheck
		_ = b.Close()     //nolint:errcheck
	}
}

// openExitOnce opens a stream on sess and runs the exit-open negotiation over
// it, returning the error (the stream is closed either way).
func openExitOnce(t *testing.T, c *Client, sess *yamux.Session) error {
	t.Helper()
	st, err := sess.Open()
	require.NoError(t, err)
	defer st.Close() //nolint:errcheck
	return c.openExit(st, []byte{0x05, 0x01, 0x00})
}

// TestExitOpenTimeoutIsCountedAndBenchesTunnel is the measured live defect: an
// exit that stops answering the SOCKS5 greeting leaves the tunnel "live" (yamux
// sees no error), so the open times out, the browser is handed nothing, and the
// stale-idle credit in pickSessionFor steers the very next stream straight back
// onto the same tunnel. The timeout must now be counted, surfaced on the status
// page, and the tunnel must sit out the next picks while another one is live.
func TestExitOpenTimeoutIsCountedAndBenchesTunnel(t *testing.T) {
	dead, closeDead := newTestSession(t) // accepts and drains: never answers the greeting
	defer closeDead()
	live, closeLive := newAnsweringSession(t)
	defer closeLive()

	now := time.Now()
	mDead, mLive := new(tunnelMeter), new(tunnelMeter)
	// The dead tunnel is the one the capacity weighing prefers, so the pick below
	// changes only because of the bench and not because of the meters.
	mDead.rxCapBps, mDead.busyAt = 5e6, now
	mLive.rxCapBps, mLive.busyAt = 1e6, now
	c := &Client{
		sessions:     []*yamux.Session{dead, live},
		recvStamp:    map[*yamux.Session]*tunnelMeter{dead: mDead, live: mLive},
		closeC:       make(chan struct{}),
		sniffTimeout: 200 * time.Millisecond,
	}

	require.Same(t, dead, c.pickSessionFor(pickRecv), "before the timeout the proven-faster tunnel wins")

	err := openExitOnce(t, c, dead)
	require.Error(t, err, "the exit never answered, so the open must fail")
	require.True(t, isTimeout(err), "the failure is a deadline expiry, got %v", err)

	require.EqualValues(t, 1, c.exitOpenTimeouts.Load(), "the client counts the timeout")
	require.EqualValues(t, 1, mDead.openTimeouts.Load(), "the tunnel it happened on is charged")
	require.True(t, mDead.onBench(time.Now()), "and is benched")
	require.False(t, mLive.onBench(time.Now()), "the other tunnel is untouched")

	// The timed-out open's stream lingers on the dead tunnel (yamux half-closes
	// it while the exit stays silent), so give the live tunnel one too: both
	// tunnels then carry the same load and the pick below turns on the bench
	// alone. Verified against unmodified develop, where this same comparison
	// returns the dead tunnel — its proven capacity outweighs everything.
	held, err := live.Open()
	require.NoError(t, err)
	defer held.Close() //nolint:errcheck
	require.Equal(t, dead.NumStreams(), live.NumStreams(), "equal load: only the bench can decide")

	// The steering fix: the next stream avoids the tunnel that just timed out.
	require.Same(t, live, c.pickSessionFor(pickRecv), "the next pick avoids the benched tunnel")
	require.Same(t, live, c.pickSession(), "for a browser conn too")

	// The status page can say so.
	snap := c.statusSnapshot()
	require.EqualValues(t, 1, snap.ExitOpenTimeouts)
	require.True(t, strings.Contains(snap.Note, "exit-open timeouts: 1"), "note = %q", snap.Note)

	// A successful open on the benched tunnel clears the bench.
	mDead.penaltyUntil.Store(time.Now().Add(exitOpenPenalty).UnixNano())
	require.NoError(t, openExitOnce(t, c, live))
	require.False(t, mLive.onBench(time.Now()))
	mDead.unbench()
	require.Same(t, dead, c.pickSessionFor(pickRecv), "once unbenched it is picked again")
}

// TestExitOpenTimeoutKeepsTheOnlyTunnel proves the bench is never applied to the
// only live tunnel: sitting out is useful only when something else can take the
// stream, and refusing to pick would fail the browser outright.
func TestExitOpenTimeoutKeepsTheOnlyTunnel(t *testing.T) {
	dead, closeDead := newTestSession(t)
	defer closeDead()

	m := new(tunnelMeter)
	c := &Client{
		sessions:     []*yamux.Session{dead},
		recvStamp:    map[*yamux.Session]*tunnelMeter{dead: m},
		closeC:       make(chan struct{}),
		sniffTimeout: 200 * time.Millisecond,
	}

	require.Error(t, openExitOnce(t, c, dead))
	require.True(t, m.onBench(time.Now()))
	require.Same(t, dead, c.pickSessionFor(pickRecv), "the only live tunnel is picked, benched or not")

	// A second timeout charges the counter again and extends the bench.
	require.Error(t, openExitOnce(t, c, dead))
	require.EqualValues(t, 2, c.exitOpenTimeouts.Load())
	require.EqualValues(t, 2, m.openTimeouts.Load())

	// Every tunnel benched but one closed: the survivor is still pickable.
	closed, closeClosed := newTestSession(t)
	closeClosed()
	c.sessions = append(c.sessions, closed)
	c.recvStamp[closed] = new(tunnelMeter)
	require.Same(t, dead, c.pickSessionFor(pickRecv), "a closed tunnel does not unbench the live one")
}

// TestExitOpenPenaltyExpires proves the bench is a short window, not a
// blacklist: past exitOpenPenalty the tunnel is picked again with no successful
// open in between (the exit may have recovered without anyone asking).
func TestExitOpenPenaltyExpires(t *testing.T) {
	a, closeA := newTestSession(t)
	defer closeA()
	b, closeB := newTestSession(t)
	defer closeB()

	mA, mB := new(tunnelMeter), new(tunnelMeter)
	now := time.Now()
	mA.rxCapBps, mA.busyAt = 5e6, now
	mB.rxCapBps, mB.busyAt = 1e6, now
	c := &Client{
		sessions:  []*yamux.Session{a, b},
		recvStamp: map[*yamux.Session]*tunnelMeter{a: mA, b: mB},
		closeC:    make(chan struct{}),
	}

	mA.bench(now.Add(-exitOpenPenalty - time.Second)) // benched, but long ago
	require.False(t, mA.onBench(time.Now()), "the bench has aged out")
	require.Same(t, a, c.pickSessionFor(pickRecv), "an expired bench does not steer anything")

	mA.bench(time.Now())
	require.Same(t, b, c.pickSessionFor(pickRecv), "a fresh bench does")
}
