// Package appnet — pkg/app/appnet/coverage_test.go: unit tests for the
// parts of the package that don't need a live router/dmsg stack: the
// Addr helpers, ErrServiceOffline, WrappedConn, the SkywireConn metric
// accessors (no-route-group path), the dmsg networker's no-op Ping, the
// top-level Ping* delegation/fallback logic (via the generated mock),
// the directConn net.Conn stubs, raw-TCP forwarding, and the generated
// MockNetworker itself.
package appnet

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// --- addr.go ---------------------------------------------------------

func TestAddrAccessors(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	a := Addr{Net: TypeSkynet, PubKey: pk, Port: 42}
	require.Equal(t, string(TypeSkynet), a.Network())
	require.Equal(t, pk, a.PK())
	require.Equal(t, pk.String()+":42", a.String())

	// Port 0 renders the "~" placeholder.
	require.Equal(t, pk.String()+":~", Addr{PubKey: pk}.String())
}

// TestConvertAddrMore covers the branches the existing TestConvertAddr
// (dmsg + routing) leaves out: the appnet.Addr passthrough and the
// unknown-type error.
func TestConvertAddrMore(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("appnet.Addr passthrough", func(t *testing.T) {
		in := Addr{Net: TypeSkynet, PubKey: pk, Port: 9}
		got, err := ConvertAddr(in)
		require.NoError(t, err)
		require.Equal(t, in, got)
	})
	t.Run("unknown type", func(t *testing.T) {
		_, err := ConvertAddr(&net.TCPAddr{})
		require.ErrorIs(t, err, ErrUnknownAddrType)
	})
}

// --- errors.go -------------------------------------------------------

func TestErrServiceOffline(t *testing.T) {
	ports := []uint16{
		skyenv.SkychatPort, skyenv.SkysocksPort, skyenv.SkysocksClientPort,
		skyenv.VPNServerPort, skyenv.VPNClientPort, 65000, // last = default branch
	}
	for _, p := range ports {
		err := ErrServiceOffline(p)
		require.Error(t, err)
		require.Contains(t, err.Error(), "offline")
	}
}

// --- wrapped_conn.go -------------------------------------------------

// fakeConn is a net.Conn whose addrs are configurable for WrapConn.
type fakeConn struct {
	net.Conn
	local, remote net.Addr
}

func (c fakeConn) LocalAddr() net.Addr  { return c.local }
func (c fakeConn) RemoteAddr() net.Addr { return c.remote }
func (c fakeConn) Close() error         { return nil }

func TestWrapConn(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	la := Addr{Net: TypeSkynet, PubKey: pk, Port: 1}
	ra := Addr{Net: TypeSkynet, PubKey: pk, Port: 2}

	wrapped, err := WrapConn(fakeConn{local: la, remote: ra})
	require.NoError(t, err)
	require.Equal(t, la, wrapped.LocalAddr())
	require.Equal(t, ra, wrapped.RemoteAddr())

	// Unconvertible local addr → error.
	_, err = WrapConn(fakeConn{local: &net.TCPAddr{}, remote: ra})
	require.ErrorIs(t, err, ErrUnknownAddrType)

	// Unconvertible remote addr → error.
	_, err = WrapConn(fakeConn{local: la, remote: &net.TCPAddr{}})
	require.ErrorIs(t, err, ErrUnknownAddrType)
}

// --- skywire_conn.go (no route group) --------------------------------

func TestSkywireConnNoRouteGroup(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close() //nolint:errcheck

	freed := false
	c := &SkywireConn{Conn: a, freePort: func() { freed = true }}

	// With nrg == nil the metric accessors return zero values, and
	// IsAlive reflects whether the underlying conn is set.
	require.True(t, c.IsAlive())
	require.Zero(t, c.Latency())
	require.Zero(t, c.UploadSpeed())
	require.Zero(t, c.DownloadSpeed())
	require.Zero(t, c.BandwidthSent())
	require.Zero(t, c.BandwidthReceived())
	require.NoError(t, c.GetError())
	require.Nil(t, c.RouteHops())
	require.Nil(t, c.RouteHopDetails())
	c.SetError(nil)            // no-op when nrg == nil
	c.SetForwardHops(nil)      // no-op when nrg == nil

	require.NoError(t, c.Close())
	require.True(t, freed, "freePort should run on Close")
	// Close is once-guarded — a second call is a no-op returning nil.
	require.NoError(t, c.Close())
}

// --- dmsg_networker.go (no dmsg client needed) -----------------------

func TestDmsgNetworkerPing(t *testing.T) {
	n := NewDMSGNetworker(nil)
	require.NotNil(t, n)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeDmsg, PubKey: pk}

	_, err := n.Ping(pk, addr)
	require.Error(t, err)
	_, err = n.PingContext(context.Background(), pk, addr)
	require.Error(t, err)
}

// --- networker.go: context helpers + Ping delegation -----------------

func TestAppNameContext(t *testing.T) {
	require.Empty(t, AppNameFromContext(nil)) //nolint:staticcheck
	require.Empty(t, AppNameFromContext(context.Background()))

	// Empty app name returns the context unchanged.
	base := context.Background()
	require.Equal(t, base, WithAppName(base, ""))

	ctx := WithAppName(base, "skychat")
	require.Equal(t, "skychat", AppNameFromContext(ctx))
}

func TestPingDelegation(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeDmsg, PubKey: pk, Port: 5}

	t.Run("no networker", func(t *testing.T) {
		ClearNetworkers()
		_, err := Ping(pk, addr)
		require.ErrorIs(t, err, ErrNoSuchNetworker)
		_, err = PingContextWithMinHops(context.Background(), pk, addr, 2)
		require.ErrorIs(t, err, ErrNoSuchNetworker)
		_, err = PingContextWithTransport(context.Background(), pk, addr, "tp")
		require.ErrorIs(t, err, ErrNoSuchNetworker)
		_, err = PingContextWithRoute(context.Background(), pk, addr, nil, nil)
		require.ErrorIs(t, err, ErrNoSuchNetworker)
	})

	t.Run("delegates to networker", func(t *testing.T) {
		ClearNetworkers()
		ctx := context.Background()
		n := &MockNetworker{}
		// Ping and all the *With* helpers fall back to PingContext when the
		// resolved networker isn't a *SkywireNetworker.
		n.On("PingContext", ctx, pk, addr).Return(nil, nil)
		require.NoError(t, AddNetworker(addr.Net, n))

		_, err := Ping(pk, addr)
		require.NoError(t, err)
		_, err = PingContextWithMinHops(ctx, pk, addr, 0) // minHops<=0 → fallback
		require.NoError(t, err)
		_, err = PingContextWithTransport(ctx, pk, addr, "") // empty tp → fallback
		require.NoError(t, err)
		_, err = PingContextWithRoute(ctx, pk, addr, nil, nil) // no hops → fallback
		require.NoError(t, err)
		n.AssertExpectations(t)
	})
}

// --- skywire_networker.go: directConn net.Conn stubs -----------------

func TestDirectConnStubs(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := &directConn{remote: pk}
	require.Equal(t, Addr{Net: TypeSkynet}, c.LocalAddr())
	require.Equal(t, Addr{Net: TypeSkynet, PubKey: pk}, c.RemoteAddr())
	require.Equal(t, pk, c.RemotePK())
	require.NoError(t, c.SetDeadline(time.Now()))
	require.NoError(t, c.SetReadDeadline(time.Now()))
	require.NoError(t, c.SetWriteDeadline(time.Now()))
}

// --- forwarding.go ---------------------------------------------------

func TestRawTCPForwarding(t *testing.T) {
	log := logging.MustGetLogger("fwd-test")
	remoteA, remoteB := net.Pipe()

	// localPort 0 → OS-assigned free port; empty network defaults to skynet.
	fwd, err := NewRawTCPForwardConn(log, "", remoteB, 8080, 0)
	require.NoError(t, err)
	require.Equal(t, "skynet", fwd.Network)

	// Registry accessors.
	require.Equal(t, fwd, GetRawTCPForwardConn(fwd.ID))
	require.Contains(t, GetAllRawTCPForwardConns(), fwd.ID)

	fwd.Serve()

	_, portStr, err := net.SplitHostPort(fwd.listener.Addr().String())
	require.NoError(t, err)
	localConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", portStr))
	require.NoError(t, err)

	// local -> remote
	go func() { _, _ = localConn.Write([]byte("ping")) }()
	got := make([]byte, 4)
	_, err = io.ReadFull(remoteA, got)
	require.NoError(t, err)
	require.Equal(t, "ping", string(got))

	// remote -> local
	go func() { _, _ = remoteA.Write([]byte("pong")) }()
	got2 := make([]byte, 4)
	_, err = io.ReadFull(localConn, got2)
	require.NoError(t, err)
	require.Equal(t, "pong", string(got2))

	require.NoError(t, fwd.Close())
	// Removed from the registry, and Close is idempotent.
	require.Nil(t, GetRawTCPForwardConn(fwd.ID))
	require.NoError(t, fwd.Close())

	_ = localConn.Close()
	_ = remoteA.Close()
}

func TestNewRawTCPForwardConnListenError(t *testing.T) {
	// Occupy a port, then ask the forwarder to bind the same one → the
	// net.Listen failure is surfaced as an error.
	busy, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer busy.Close() //nolint:errcheck
	_, portStr, err := net.SplitHostPort(busy.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	_, remoteB := net.Pipe()
	defer remoteB.Close() //nolint:errcheck
	_, err = NewRawTCPForwardConn(logging.MustGetLogger("fwd-err"), "skynet", remoteB, 1, port)
	require.Error(t, err)
}

func TestRawTCPForwardingRegistry(t *testing.T) {
	fwd := &RawTCPForwardConn{ID: uuid.New(), Network: "dmsg"}
	AddRawTCPForwarding(fwd)
	require.Equal(t, fwd, GetRawTCPForwardConn(fwd.ID))
	RemoveRawTCPForwardConn(fwd.ID)
	require.Nil(t, GetRawTCPForwardConn(fwd.ID))
}

// --- mock_networker.go (generated) -----------------------------------

func TestMockNetworker(t *testing.T) {
	ctx := context.Background()
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk}

	m := &MockNetworker{}
	m.On("Dial", addr).Return(nil, nil)
	m.On("DialContext", ctx, addr).Return(nil, nil)
	m.On("Listen", addr).Return(nil, nil)
	m.On("ListenContext", ctx, addr).Return(nil, nil)
	m.On("Ping", pk, addr).Return(nil, nil)
	m.On("PingContext", ctx, pk, addr).Return(nil, nil)

	_, _ = m.Dial(addr)
	_, _ = m.DialContext(ctx, addr)
	_, _ = m.Listen(addr)
	_, _ = m.ListenContext(ctx, addr)
	_, _ = m.Ping(pk, addr)
	_, _ = m.PingContext(ctx, pk, addr)
	m.AssertExpectations(t)
}
