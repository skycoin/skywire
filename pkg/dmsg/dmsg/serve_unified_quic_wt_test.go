//go:build !tinygo && !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/serve_unified_quic_wt_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestUnifiedQUICWTSessions covers the production UDP path (api.go →
// ServeUnifiedQUIC): dmsg-over-QUIC and dmsg-over-WebTransport ALPN-demuxed on
// ONE socket. The pre-existing QUIC/WT tests each exercise a dedicated
// listener (ServeQUIC / ServeWebTransport), so a regression that broke one
// ALPN branch of the SHARED listener — what every deployed server actually
// runs — was invisible to the suite.
func TestUnifiedQUICWTSessions(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	// A fast UpdateInterval matters: the FIRST self-registration posts only the
	// TCP address — the optional endpoints (UDP/WS/WT) are merged in by the
	// periodic read-modify-write refresh, which defaults to one minute.
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 300 * time.Millisecond}, nil)
	srv.SetLogger(logging.MustGetLogger("unified_server"))

	// TCP listener: Serve owns the discovery self-registration loop, exactly
	// like production (api.go runs Serve and ServeUnifiedQUIC side by side).
	tcpLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)

	wtURL := "https://" + udpConn.LocalAddr().String() + wtPath
	chSrv := make(chan error, 2)
	go func() { chSrv <- srv.Serve(tcpLis, tcpLis.Addr().String()) }()
	go func() { chSrv <- srv.ServeUnifiedQUIC(udpConn, udpConn.LocalAddr().String(), wtURL) }()
	t.Cleanup(func() {
		require.NoError(t, srv.Close())
		// Drain the serve goroutines best-effort: teardown latency (e.g. the WT
		// h3 server unwinding) must not hang the suite past its deadline.
		for i := 0; i < 2; i++ {
			select {
			case err := <-chSrv:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				return
			}
		}
	})

	// Wait until the self-registered entry advertises BOTH UDP (quic) and WT.
	require.Eventually(t, func() bool {
		e, eerr := dc.Entry(context.Background(), pkSrv)
		return eerr == nil && e.Server != nil &&
			e.Protocol == "quic" && e.Server.AddressUDP != "" &&
			e.Server.AddressWT != "" && e.Server.CertHashWT != ""
	}, 10*time.Second, 50*time.Millisecond, "server entry never advertised quic+wt")

	// One client forced onto each carrier of the shared socket.
	newClient := func(name, carrier string) *Client {
		conf := DefaultConfig()
		conf.Carriers = []string{carrier}
		pk, sk := GenKeyPair(t, name)
		c := NewClient(pk, sk, dc, conf)
		c.SetLogger(logging.MustGetLogger(name))
		go c.Serve(context.Background())
		t.Cleanup(func() { require.NoError(t, c.Close()) })
		return c
	}
	quicClient := newClient("quic_client", CarrierQUIC)
	wtClient := newClient("wt_client", CarrierWT)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Raw dmsg-over-QUIC session on the shared listener.
	require.NoError(t, quicClient.EnsureSession(ctx, entrySnapshot(t, dc, pkSrv)),
		"raw dmsg-QUIC session over the unified listener")

	// WebTransport session on the same UDP socket.
	require.NoError(t, wtClient.EnsureSession(ctx, entrySnapshot(t, dc, pkSrv)),
		"WebTransport session over the unified listener")

	// A stream through the server proves both sessions relay, not just dial:
	// wtClient listens, quicClient dials it through the shared-socket server.
	lis, err := wtClient.Listen(49152)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lis.Close()) })

	accepted := make(chan error, 1)
	go func() {
		s, aerr := lis.AcceptStream()
		if aerr == nil {
			_ = s.Close() //nolint:errcheck
		}
		accepted <- aerr
	}()

	stream, err := quicClient.DialStream(ctx, Addr{PK: wtClient.LocalPK(), Port: 49152})
	require.NoError(t, err, "stream QUIC client → WT client through the unified server")
	require.NoError(t, stream.Close())
	require.NoError(t, <-accepted)
}

// entrySnapshot fetches pk's current entry from the mock discovery.
func entrySnapshot(t *testing.T, dc disc.APIClient, pk cipher.PubKey) *disc.Entry {
	t.Helper()
	e, err := dc.Entry(context.Background(), pk)
	require.NoError(t, err)
	return e
}
