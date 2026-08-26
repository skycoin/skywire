// Package skysocks multi-tunnel (connection-striping) client tests.
package skysocks

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
)

// leastLoaded is the pure striping policy: pick the smallest non-negative count
// (a closed/skipped tunnel is the -1 sentinel), ties to the lowest index.
func TestLeastLoaded(t *testing.T) {
	cases := []struct {
		name   string
		counts []int
		want   int
	}{
		{"empty", nil, -1},
		{"single", []int{0}, 0},
		{"all-closed", []int{-1, -1, -1}, -1},
		{"pick-min", []int{5, 2, 8}, 1},
		{"zero-wins", []int{2, 1, 0}, 2},
		{"ties-lowest-index", []int{3, 3, 3}, 0},
		{"skip-closed", []int{-1, 3, 1, -1}, 2},
		{"closed-min-ignored", []int{-1, 0}, 1},
		{"first-live-when-tie-after-closed", []int{-1, 2, 2}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, leastLoaded(tc.counts))
		})
	}
}

// newTestSession returns a live yamux client session whose peer drains every
// accepted stream, so Open() completes and NumStreams() reflects opens without
// wedging. The cleanup tears both sides down.
func newTestSession(t *testing.T) (*yamux.Session, func()) {
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
			go io.Copy(io.Discard, st) //nolint:errcheck
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

// pickSession returns the least-loaded LIVE tunnel and skips closed ones. Build a
// Client with three real sessions of differing open-stream counts and verify the
// selection, then that closing tunnels re-routes and finally yields nil.
func TestPickSession_LeastLoadedAndSkipsClosed(t *testing.T) {
	s0, c0 := newTestSession(t)
	defer c0()
	s1, c1 := newTestSession(t)
	defer c1()
	s2, c2 := newTestSession(t)
	defer c2()

	// s0=2 streams, s1=1, s2=0 → least-loaded is s2.
	for i := 0; i < 2; i++ {
		_, err := s0.Open()
		require.NoError(t, err)
	}
	_, err := s1.Open()
	require.NoError(t, err)

	c := &Client{sessions: []*yamux.Session{s0, s1, s2}, closeC: make(chan struct{})}
	require.Same(t, s2, c.pickSession(), "empty tunnel is least-loaded")

	// Retire s2 → the next-least-loaded live tunnel (s1) is picked.
	require.NoError(t, s2.Close())
	require.Same(t, s1, c.pickSession(), "closed tunnel skipped, s1 next")

	// Retire the rest → no live tunnel → nil (the route-down trigger).
	require.NoError(t, s0.Close())
	require.NoError(t, s1.Close())
	require.Nil(t, c.pickSession(), "all tunnels closed → nil")
	require.True(t, c.allSessionsClosed())
	require.False(t, c.anySessionLive())
}

// A multi-session Client stripes accepted conns across its tunnels: repeatedly
// selecting the least-loaded tunnel and opening a stream on it spreads N opens
// evenly across N tunnels (the accept loop's per-conn behavior).
func TestPickSession_StripesEvenlyAcrossTunnels(t *testing.T) {
	const n = 3
	sessions := make([]*yamux.Session, n)
	for i := 0; i < n; i++ {
		s, cleanup := newTestSession(t)
		defer cleanup()
		sessions[i] = s
	}
	c := &Client{sessions: sessions, closeC: make(chan struct{})}

	// 9 conns over 3 tunnels via least-loaded selection → 3 each.
	for i := 0; i < 3*n; i++ {
		s := c.pickSession()
		require.NotNil(t, s)
		_, err := s.Open()
		require.NoError(t, err)
	}
	for i, s := range sessions {
		require.Equal(t, 3, s.NumStreams(), "tunnel %d should carry an even share", i)
	}
	require.Equal(t, 3*n, c.totalStreams(), "totalStreams sums across tunnels")
}

// AddTunnel / NewMultiClient grow the tunnel set; a single-conn NewMultiClient is
// equivalent to NewClient (one tunnel).
func TestNewMultiClient_And_AddTunnel(t *testing.T) {
	mk := func(t *testing.T) (net.Conn, func()) {
		t.Helper()
		a, b := net.Pipe()
		go func() {
			sess, e := yamux.Server(b, yamux.DefaultConfig())
			if e == nil {
				for {
					if _, ae := sess.Accept(); ae != nil {
						return
					}
				}
			}
		}()
		return a, func() { _ = a.Close(); _ = b.Close() } //nolint:errcheck
	}

	ca, closeA := mk(t)
	defer closeA()
	single, err := NewMultiClient([]net.Conn{ca}, nil)
	require.NoError(t, err)
	require.Len(t, single.snapshotSessions(), 1)
	require.NoError(t, single.Close())

	cb, closeB := mk(t)
	defer closeB()
	cc, closeC := mk(t)
	defer closeC()
	multi, err := NewMultiClient([]net.Conn{cb, cc}, nil)
	require.NoError(t, err)
	require.Len(t, multi.snapshotSessions(), 2)

	cd, closeD := mk(t)
	defer closeD()
	require.NoError(t, multi.AddTunnel(cd))
	require.Len(t, multi.snapshotSessions(), 3)
	require.NoError(t, multi.Close())

	// Zero conns is an error.
	_, err = NewMultiClient(nil, nil)
	require.Error(t, err)
}
