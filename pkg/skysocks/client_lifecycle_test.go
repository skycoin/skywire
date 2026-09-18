// Package skysocks pkg/skysocks/client_lifecycle_test.go
package skysocks

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"
)

// A retired tunnel's session pointer leaves the slice with it. Nothing else
// prunes c.sessions, so a dead yamux session used to be pinned — stream map,
// recv buffers and all — for the life of the client, and pickSessionFor walked
// every corpse, under yamux's locks, on every stream open.
func TestRetireTunnel_CompactsTheSessionSlice(t *testing.T) {
	c, s, cleanup := failoverClient(t, 200, 40)
	defer cleanup()
	active, slow, fast := s[0], s[1], s[2]

	require.Len(t, c.sessions, 3)
	require.True(t, c.retireTunnel(active, "liveness: no pong and no bytes for 47s"))

	held := c.snapshotSessions()
	require.Len(t, held, 2)
	require.NotContains(t, held, active, "the dead session is gone from the slice")
	require.Contains(t, held, slow)
	require.Contains(t, held, fast)

	// The survivors keep their identities and their state: the promote that
	// came with the retire still holds, and the picker still finds them.
	require.False(t, c.IsStandby(fast))
	require.True(t, c.IsStandby(slow))
	require.Same(t, fast, c.pickSessionFor(pickAny))

	// A standby death compacts the same way, and retiring the last tunnel
	// leaves an empty — not a corpse-filled — set.
	require.True(t, c.retireTunnel(slow, "tunnel session closed"))
	require.Len(t, c.snapshotSessions(), 1)
	require.True(t, c.retireTunnel(fast, "tunnel session closed"))
	require.Empty(t, c.snapshotSessions())
	require.True(t, c.allSessionsClosed())
}

// A dial that lands after Close must not append a live route group to a closed
// client: nothing would ever close it (the keepalive loop has returned and
// --reconnect builds a whole new Client), so the group would stay registered on
// the exit and the setup node until its rules expired.
func TestAddTunnel_RefusedAfterClose(t *testing.T) {
	c, _, cleanup := failoverClient(t, 40)
	defer cleanup()

	require.NoError(t, c.Close())

	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck
	go func() {
		srv, err := yamux.Server(b, yamux.DefaultConfig())
		if err != nil {
			return
		}
		defer srv.Close() //nolint:errcheck
		for {
			st, e := srv.Accept()
			if e != nil {
				return
			}
			go io.Copy(io.Discard, st) //nolint:errcheck
		}
	}()

	before := len(c.snapshotSessions())
	require.ErrorIs(t, c.AddTunnel(a), ErrClientClosed)
	require.ErrorIs(t, c.AddStandbyTunnel(a), ErrClientClosed)
	require.Len(t, c.snapshotSessions(), before, "nothing was appended behind Close")
}

// The pool fill and the re-dial backoff are armed by the death itself, not by
// the keepalive loop's 15 s level sample: a death whose replacement lands
// inside the same window leaves the level unchanged, and a pool already
// settled at its ceiling would stay one tunnel short for good.
func TestRetireTunnel_ArmsThePoolFill(t *testing.T) {
	c, s, cleanup := failoverClient(t, 40)
	defer cleanup()

	c.SetStandbyPool(4)
	c.settlePool(2, "pool ceiling")
	_, _, settled, _, _ := c.StandbyPoolState()
	require.True(t, settled)

	c.redialMu.Lock()
	c.redialFails = maxRedialFails
	c.redialMu.Unlock()

	require.True(t, c.retireTunnel(s[0], "liveness: no pong and no bytes for 47s"))

	_, _, settled, _, _ = c.StandbyPoolState()
	require.False(t, settled, "the death re-armed the fill")
	c.redialMu.Lock()
	fails := c.redialFails
	armed := c.poolArmed
	c.redialMu.Unlock()
	require.Zero(t, fails, "and the re-dial backoff")
	require.True(t, armed)
}

// zeroReader is the broken body the guard exists for: it never blocks, never
// errors and never delivers a byte.
type zeroReader struct{ reads int }

func (z *zeroReader) Read(_ []byte) (int, error) { z.reads++; return 0, nil }

type nopDeadliner struct{}

func (nopDeadliner) SetReadDeadline(_ time.Time) error { return nil }

// A chunk body returning (0, nil) forever must fail the fetch rather than peg a
// core: no read blocks, so the rolling deadline never fires and the chunk would
// otherwise never complete nor fail, with writeInOrder waiting on it holding
// its memory permits.
func TestReadChunkBody_ZeroReadsTerminate(t *testing.T) {
	z := &zeroReader{}
	done := make(chan error, 1)
	go func() {
		_, err := readChunkBody(nopDeadliner{}, z, make([]byte, 4<<10), time.Minute)
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
		require.Contains(t, err.Error(), "zero-byte reads")
	case <-time.After(5 * time.Second):
		t.Fatal("readChunkBody spun on a zero-read body instead of failing")
	}
	require.Less(t, z.reads, 100, "the streak is bounded")
}

// A body that delivers is unaffected: the guard resets on every byte.
func TestReadChunkBody_SlowBodyCompletes(t *testing.T) {
	buf := make([]byte, 8)
	n, err := readChunkBody(nopDeadliner{}, &drip{n: len(buf)}, buf, time.Minute)
	require.NoError(t, err)
	require.Equal(t, len(buf), n, "a completed body reports every byte it filled")
}

// drip returns one byte per read, with a zero-byte read between each.
type drip struct{ n, sent int }

func (d *drip) Read(p []byte) (int, error) {
	if d.sent >= d.n {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	d.sent++
	p[0] = 'x'
	return 1, nil
}
