package visor

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/skyroute"
)

// TestSkyForwardingMux_PoolReuseOverRealServer wires the REAL yamux mux forwarding
// server (serveSkyForwardingMuxSession, the accept half) to the REAL skyroute.Pool
// (the client half) over a single in-memory conn, and verifies that many logical
// connections reuse ONE route group and each reaches the registered service through
// the real handleServerConn handshake+dispatch.
//
// A conn is a conn: a router RouteGroup already satisfies net.Conn and is run under
// yamux in production by skysocks-lite, so exercising the mux server + pool over a
// net.Pipe covers the integration the skyroute unit tests (which mock the far end)
// do not — without standing up the full transport/route-setup stack.
func TestSkyForwardingMux_PoolReuseOverRealServer(t *testing.T) {
	const echoPort uint16 = 4321

	// A minimal visor: the service-registry happy path in handleServerConn only
	// needs v.services. Register an echo service on echoPort.
	reg := NewServiceRegistry()
	reg.Register(echoPort, "echo", func(c net.Conn) {
		_, _ = io.Copy(c, c) //nolint:errcheck
		_ = c.Close()        //nolint:errcheck
	})
	v := &Visor{services: reg}
	log := logging.MustGetLogger("skyfwd_mux_test")

	// One route group == one net.Pipe. The server yamux-serves its end; the pool
	// yamux-dials the other end (once) and multiplexes streams over it.
	serverConn, clientConn := net.Pipe()
	go serveSkyForwardingMuxSession(log, serverConn, v)

	var dials int32
	dial := func(_ context.Context, _ uint16) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		return clientConn, nil // the single held route group
	}
	pool := skyroute.New(time.Minute, nil)
	defer pool.Close() //nolint:errcheck

	dest, _ := cipher.GenerateKeyPair()

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stream, err := pool.OpenStream(ctx, dest.Hex(), dial)
		require.NoError(t, err, "open stream %d", i)
		require.NoError(t, skynetweb.PerformHandshake(stream, echoPort), "handshake %d", i)

		msg := []byte("ping")
		_, err = stream.Write(msg)
		require.NoError(t, err)
		buf := make([]byte, len(msg))
		_, err = io.ReadFull(stream, buf)
		require.NoError(t, err, "echo read %d", i)
		require.Equal(t, "ping", string(buf))

		_ = stream.Close() //nolint:errcheck
		cancel()
	}

	require.EqualValues(t, 1, atomic.LoadInt32(&dials),
		"all 5 connections must reuse ONE route group, not re-dial")
}

// TestMuxPeerConn_CarriesPK asserts the yamux-stream wrapper reports the route
// group's peer PK — the property that keeps the per-port whitelist working over the
// muxed path (remotePKFromForwardingConn prefers RemotePK()).
func TestMuxPeerConn_CarriesPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck
	c := &muxPeerConn{Conn: a, pk: pk}
	got, ok := remotePKFromForwardingConn(c)
	require.True(t, ok)
	require.Equal(t, pk, got)
}
