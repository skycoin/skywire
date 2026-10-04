// Package skysocks pkg/skysocks/localresolver_test.go
package skysocks

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

func resolverSet(t *testing.T, dial func(routing.Port) (net.Conn, error)) *LocalResolvers {
	t.Helper()
	return NewLocalResolvers([]appnet.LocalService{
		{Port: routing.Port(4445), Label: "dmsg_web", Suffixes: []string{".dmsg"}},
		{Port: routing.Port(4446), Label: "skynet_web", Suffixes: []string{".skynet"}},
		{Port: routing.Port(80), Label: "log_server"}, // no suffix: answers for no name
	}, dial)
}

func TestLocalResolversPortFor(t *testing.T) {
	r := resolverSet(t, func(routing.Port) (net.Conn, error) { return nil, nil })
	require.NotNil(t, r)

	for _, tc := range []struct {
		host string
		port routing.Port
		ok   bool
	}{
		{host: "0279be2ff4a1b5f0b3b7e2b0b2e4f1b6d4e9c8a7b6c5d4e3f2a1b0c9d8e7f6a5b4.skynet", port: 4446, ok: true},
		{host: "status.skynet", port: 4446, ok: true},
		{host: "SKYWIRE.SKYNET", port: 4446, ok: true},
		{host: "tpd.dmsg", port: 4445, ok: true},
		{host: "tpd.dmsg.", port: 4445, ok: true}, // a fully-qualified name
		{host: "deep.sub.domain.skynet", port: 4446, ok: true},
		{host: "example.com", ok: false},
		{host: "notskynet", ok: false},
		{host: "skynet", ok: false},  // only the suffix: no destination label
		{host: ".skynet", ok: false}, // same, with the dot spelled out
		{host: "example.skynet.com", ok: false},
		{host: "", ok: false},
	} {
		port, ok := r.PortFor(tc.host)
		require.Equal(t, tc.ok, ok, tc.host)
		if tc.ok {
			require.Equal(t, tc.port, port, tc.host)
		}
	}
}

// A nil set is the "no resolver published" case every caller holds unguarded.
func TestLocalResolversNil(t *testing.T) {
	var r *LocalResolvers
	_, ok := r.PortFor("x.skynet")
	require.False(t, ok)
	require.Nil(t, r.Suffixes())
	_, err := r.Dial(routing.Port(4446))
	require.Error(t, err)
}

func TestNewLocalResolversKeepsOnlyNameAnswerers(t *testing.T) {
	dial := func(routing.Port) (net.Conn, error) { return nil, nil }

	// No dial: nothing can be reached, so there is no set.
	require.Nil(t, NewLocalResolvers([]appnet.LocalService{
		{Port: routing.Port(4446), Suffixes: []string{".skynet"}},
	}, nil))

	// Services that answer for no name cannot be matched by a hostname.
	require.Nil(t, NewLocalResolvers([]appnet.LocalService{
		{Port: routing.Port(80), Label: "log_server"},
	}, dial))

	require.Equal(t, []string{".dmsg", ".skynet"}, resolverSet(t, dial).Suffixes())
}

// Open replays what the browser sent and consumes the resolver's method reply,
// leaving the resolver's CONNECT reply for the caller's splice. The exchange is
// sequential: a pipelined request would deadlock against the unbuffered pipe a
// local service is served over (the resolver replies before reading it).
func TestLocalResolversOpenReplaysTheHandshake(t *testing.T) {
	resolverEnd, clientEnd := net.Pipe()
	r := resolverSet(t, func(port routing.Port) (net.Conn, error) {
		require.Equal(t, routing.Port(4446), port)
		return clientEnd, nil
	})

	greeting := []byte{0x05, 0x01, 0x00}
	req := []byte{0x05, 0x01, 0x00, 0x03, 0x0b}
	req = append(req, "x.skynet:80"[:11]...)
	req = append(req, 0x00, 0x50)

	type result struct {
		conn net.Conn
		err  error
	}
	res := make(chan result, 1)
	go func() {
		conn, err := r.Open(routing.Port(4446), greeting, req)
		res <- result{conn, err}
	}()

	// The greeting arrives first, on its own: the resolver answers it before the
	// request is sent, exactly as go-socks5 does.
	_ = resolverEnd.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	gotGreeting := make([]byte, len(greeting))
	_, err := io.ReadFull(resolverEnd, gotGreeting)
	require.NoError(t, err)
	require.Equal(t, greeting, gotGreeting)

	_, err = resolverEnd.Write([]byte{0x05, 0x00})
	require.NoError(t, err)

	// Then the CONNECT request, byte-for-byte.
	gotReq := make([]byte, len(req))
	_, err = io.ReadFull(resolverEnd, gotReq)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	r1 := <-res
	require.NoError(t, r1.err)
	require.NotNil(t, r1.conn)

	// The CONNECT reply is NOT consumed by Open — the browser gets it. net.Pipe is
	// synchronous, so the write has to be in flight while the read runs.
	go func() {
		_, _ = resolverEnd.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck
	}()
	reply := make([]byte, 10)
	_ = r1.conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, err = io.ReadFull(r1.conn, reply)
	require.NoError(t, err)
	require.Equal(t, byte(0x00), reply[1])
}

func TestLocalResolversOpenRejectsABadMethodReply(t *testing.T) {
	resolverEnd, clientEnd := net.Pipe()
	r := resolverSet(t, func(routing.Port) (net.Conn, error) { return clientEnd, nil })

	errC := make(chan error, 1)
	go func() {
		_, err := r.Open(routing.Port(4446), []byte{0x05, 0x01, 0x00}, []byte{0x05, 0x01})
		errC <- err
	}()

	_ = resolverEnd.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, _ = io.ReadFull(resolverEnd, make([]byte, 3))             //nolint:errcheck // the greeting
	// 0xFF: no acceptable method.
	_, err := resolverEnd.Write([]byte{0x05, 0xFF})
	require.NoError(t, err)

	select {
	case err := <-errC:
		require.ErrorContains(t, err, "non-no-auth")
	case <-time.After(5 * time.Second):
		t.Fatal("Open never returned")
	}
}

// A dial failure surfaces rather than silently falling back to the exit, which
// could not reach a mesh name anyway.
func TestLocalResolversOpenSurfacesADialFailure(t *testing.T) {
	r := resolverSet(t, func(routing.Port) (net.Conn, error) { return nil, io.ErrClosedPipe })
	_, err := r.Open(routing.Port(4446), []byte{0x05}, []byte{0x05})
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

// A client that starts before any resolver published picks one up on the
// keepalive tick, instead of waiting for the next reconnect cycle.
func TestPullLocalResolversPicksUpALateResolver(t *testing.T) {
	c := &Client{}
	require.Nil(t, c.localResolvers())

	var calls int
	published := false
	c.SetLocalResolverRefresh(func() *LocalResolvers {
		calls++
		if !published {
			return nil
		}
		return resolverSet(t, func(routing.Port) (net.Conn, error) { return nil, nil })
	})

	now := time.Now()
	c.pullLocalResolvers(now)
	require.Equal(t, 1, calls)
	require.Nil(t, c.localResolvers(), "nothing published yet")

	// Re-asking is rate-limited while still empty.
	c.pullLocalResolvers(now.Add(time.Second))
	require.Equal(t, 1, calls)

	published = true
	c.pullLocalResolvers(now.Add(localResolverRefreshInterval))
	require.Equal(t, 2, calls)
	require.NotNil(t, c.localResolvers())

	// With a set in hand it stops asking: a change lands on the next cycle.
	c.pullLocalResolvers(now.Add(10 * localResolverRefreshInterval))
	require.Equal(t, 2, calls)
}
