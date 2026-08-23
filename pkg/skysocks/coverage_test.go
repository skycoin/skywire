// Package skysocks additional client/server coverage tests.
package skysocks

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
)

// addrConn wraps a net.Conn but reports a caller-supplied RemoteAddr, letting a
// test drive the server's whitelist path without a real mesh transport.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (a addrConn) RemoteAddr() net.Addr { return a.remote }

// chanListener hands out a fixed set of conns then blocks until Close, so
// Server.Serve can be exercised deterministically over a known sequence.
type chanListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newChanListener(conns ...net.Conn) *chanListener {
	l := &chanListener{conns: make(chan net.Conn, len(conns)), closed: make(chan struct{})}
	for _, c := range conns {
		l.conns <- c
	}
	return l
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return testAddr{} }

func wlPK(b byte) cipher.PubKey {
	var pk cipher.PubKey
	pk[0] = 0x02
	pk[1] = b
	return pk
}

// IsPublic reflects whether a whitelist was configured.
func TestServer_IsPublic(t *testing.T) {
	pub, err := NewServer(nil, nil)
	require.NoError(t, err)
	require.True(t, pub.IsPublic(), "no whitelist → public")

	priv, err := NewServer([]cipher.PubKey{wlPK(1)}, nil)
	require.NoError(t, err)
	require.False(t, priv.IsPublic(), "whitelist set → not public")
}

// getRemotePK returns the PK for an appnet.Addr conn and errors for an
// un-convertible (plain TCP) address.
func TestServer_GetRemotePK(t *testing.T) {
	s, err := NewServer(nil, nil)
	require.NoError(t, err)

	want := wlPK(7)
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck

	wrapped := addrConn{Conn: c1, remote: appnet.Addr{Net: appnet.TypeSkynet, PubKey: want, Port: routing.Port(3)}}
	got, err := s.getRemotePK(wrapped)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// A plain pipe conn (fake non-convertible addr) yields an error.
	_, err = s.getRemotePK(addrConn{Conn: c1, remote: testAddr{}})
	require.Error(t, err)
}

// Serve rejects a connection whose PK is not in the whitelist (conn closed,
// loop continues) and rejects one whose address can't be resolved.
func TestServer_Serve_WhitelistRejects(t *testing.T) {
	allowed := wlPK(1)
	s, err := NewServer([]cipher.PubKey{allowed}, nil)
	require.NoError(t, err)

	// Conn 1: PK not in whitelist → rejected+closed.
	notAllowed := wlPK(2)
	r1a, r1b := net.Pipe()
	defer r1b.Close() //nolint:errcheck
	c1 := addrConn{Conn: r1a, remote: appnet.Addr{Net: appnet.TypeSkynet, PubKey: notAllowed, Port: routing.Port(1)}}

	// Conn 2: address not convertible → getRemotePK fails → rejected+closed.
	r2a, r2b := net.Pipe()
	defer r2b.Close() //nolint:errcheck
	c2 := addrConn{Conn: r2a, remote: testAddr{}}

	l := newChanListener(c1, c2)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(l) }()

	// Both rejected conns must be closed by the server.
	requireClosed(t, r1a)
	requireClosed(t, r2a)

	require.NoError(t, s.Close())
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

// Serve admits a whitelisted connection: it passes the whitelist check and a
// yamux server is started over it (the client half completes a yamux handshake).
func TestServer_Serve_WhitelistAdmits(t *testing.T) {
	allowed := wlPK(1)
	s, err := NewServer([]cipher.PubKey{allowed}, nil)
	require.NoError(t, err)

	srvSide, cliSide := net.Pipe()
	admitted := addrConn{Conn: srvSide, remote: appnet.Addr{Net: appnet.TypeSkynet, PubKey: allowed, Port: routing.Port(1)}}

	l := newChanListener(admitted)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(l) }()

	// The server side runs yamux.Server; a yamux client on the other end must
	// handshake successfully, proving the conn was admitted (not closed).
	done := make(chan error, 1)
	go func() {
		sess, e := yamux.Client(cliSide, yamux.DefaultConfig())
		if e != nil {
			done <- e
			return
		}
		_, e = sess.Ping()
		done <- e
	}()

	select {
	case e := <-done:
		require.NoError(t, e, "whitelisted conn should be admitted and yamux-served")
	case <-time.After(3 * time.Second):
		t.Fatal("yamux handshake over admitted conn timed out")
	}

	_ = cliSide.Close() //nolint:errcheck
	require.NoError(t, s.Close())
	<-errCh
}

// Serve returns nil immediately when the server is already closed.
func TestServer_Serve_AlreadyClosed(t *testing.T) {
	s, err := NewServer(nil, nil)
	require.NoError(t, err)
	l := newChanListener()
	s.close() // mark closed before Serve loops
	// Serve needs a listener to store; give it one and expect an immediate nil.
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(l) }()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve on a closed server should return nil immediately")
	}
	require.NoError(t, l.Close())
}

// Server.Close on a nil receiver is safe.
func TestServer_CloseNil(t *testing.T) {
	var s *Server
	require.NoError(t, s.Close())
}

// ListenAndServe surfaces a listen error via the returned error.
func TestClient_ListenAndServe_ListenError(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	go func() {     // minimal yamux server so NewClient succeeds
		sess, e := yamux.Server(b, yamux.DefaultConfig())
		if e == nil {
			_, _ = sess.Accept() //nolint:errcheck
		}
	}()
	c, err := NewClient(a, nil)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	// An unbindable address forces net.Listen to fail.
	err = c.ListenAndServe("256.256.256.256:99999")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listen")
}

// Client.Close on a nil receiver is safe; Close on a live client is idempotent.
func TestClient_CloseNilAndIdempotent(t *testing.T) {
	var nilC *Client
	require.NoError(t, nilC.Close())

	a, b := net.Pipe()
	defer b.Close() //nolint:errcheck
	go func() {
		sess, e := yamux.Server(b, yamux.DefaultConfig())
		if e == nil {
			_, _ = sess.Accept() //nolint:errcheck
		}
	}()
	c, err := NewClient(a, nil)
	require.NoError(t, err)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close(), "second Close is a no-op via sync.Once")
}

// When the yamux session is dead, ListenAndServe accepts a connection, its
// session.Open fails, it serves the route-down interstitial, and tears the
// client down — surfacing the stream-open error to the caller.
func TestClient_ListenAndServe_SessionOpenError(t *testing.T) {
	cconn, sconn := net.Pipe()
	// Bring up a real server session then close it so client stream-open fails.
	srvSess, err := yamux.Server(sconn, yamux.DefaultConfig())
	require.NoError(t, err)

	c, err := NewClient(cconn, nil)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	// Kill the session so c.session.Open() fails inside the accept loop.
	require.NoError(t, srvSess.Close())
	require.NoError(t, sconn.Close())

	addr := freeAddr(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- c.ListenAndServe(addr) }()

	// Drive one browser connection into the accept loop once it is listening.
	require.Eventually(t, func() bool {
		conn, derr := net.Dial("tcp", addr)
		if derr != nil {
			return false
		}
		_ = conn.Close() //nolint:errcheck
		return true
	}, 3*time.Second, 20*time.Millisecond)

	select {
	case err := <-serveErr:
		require.Error(t, err, "ListenAndServe should exit with the stream-open error")
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not exit after session-open failure")
	}
}

// freeAddr returns a currently-free loopback address (host:port).
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// requireClosed asserts a pipe conn was closed by the peer within a short window.
func requireClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	buf := make([]byte, 1)
	_, err := c.Read(buf)
	require.Error(t, err, "expected conn to be closed by server")
}
