// Package dmsg pkg/dmsg/dmsg/serve_unified_test.go
package dmsg

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestServeWithWS_RawAndWSOnOnePort proves the protocol-unification: a single
// server listener serves BOTH raw-dmsg (TCP) and dmsg-over-WebSocket. A TCP
// client and a WS client both connect to the SAME server on the SAME port (the
// entry advertises Address + AddressWS pointing at it), and a stream bridges from
// the TCP client to the WS client through the unified server.
func TestServeWithWS_RawAndWSOnOnePort(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("unified_server"))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr := lis.Addr().String()
	wsURL := "ws://" + tcpAddr + wsPath

	// Publish the server entry advertising BOTH Address (TCP) and AddressWS — the
	// SAME host:port — so a raw-TCP client and a WS client both target this one
	// listener. Post it BEFORE starting ServeWithWS: ServeWithWS's own Serve
	// self-registration also creates the seq-0 server entry, so racing a manual
	// post against it made one side lose the discovery sequence check ("sequence
	// field of new entry is not sequence of old entry + 1"). Posting first makes
	// the self-registration a no-delta no-op (it reads back this exact entry —
	// same Address + AddressWS via setAdvertisedWSAddr), so the unification under
	// test (the cmux demux) is exercised deterministically without the flake.
	srvEntry := disc.NewServerEntry(pkSrv, 0, tcpAddr, maxSessions)
	srvEntry.Server.AddressWS = wsURL
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.ServeWithWS(lis, tcpAddr, wsURL) }()

	// Raw-TCP client (default carrier).
	pkA, skA := GenKeyPair(t, "tcp client")
	clientA := NewClient(pkA, skA, dc, DefaultConfig())
	clientA.SetLogger(logging.MustGetLogger("tcp_client"))
	go clientA.Serve(context.Background())

	// WS client (CarrierWS → dials the AddressWS over WebSocket).
	wsConf := DefaultConfig()
	wsConf.Carriers = []string{CarrierWS}
	pkB, skB := GenKeyPair(t, "ws client")
	clientB := NewClient(pkB, skB, dc, wsConf)
	clientB.SetLogger(logging.MustGetLogger("ws_client"))
	go clientB.Serve(context.Background())

	// Both clients must hold a session to THIS unified server at the same time.
	// Poll the specific per-server session (not just SessionCount): on a cold start
	// a freshly-connected session can drop and re-dial ("yamux ping: read: EOF"),
	// so a one-shot Session(pkSrv) check taken right after SessionCount>0 flakes —
	// the session it counted may already be mid-reconnect. Retrying the exact check
	// absorbs that churn.
	require.Eventually(t, func() bool {
		_, okA := clientA.Session(pkSrv)
		_, okB := clientB.Session(pkSrv)
		return okA && okB
	}, 15*time.Second, 200*time.Millisecond, "raw + WS clients failed to hold a session to the same unified server")

	// Bridge a stream from the TCP client to the WS client THROUGH the one server.
	const port = 8080
	lisB, err := clientB.Listen(port)
	require.NoError(t, err)
	defer lisB.Close() //nolint:errcheck

	// DialStream reads pkB's discovery entry to pick a delegated server. Right
	// after connecting, pkB may not have published that entry yet ("dmsg error 103
	// - client entry in discovery has no delegated servers"), so retry until it has
	// propagated. Also absorbs a mid-test session re-dial.
	var connA *Stream
	require.Eventually(t, func() bool {
		var derr error
		connA, derr = clientA.DialStream(context.TODO(), Addr{PK: pkB, Port: port})
		return derr == nil
	}, 15*time.Second, 200*time.Millisecond, "DialStream A->B never succeeded (pkB discovery-entry propagation)")
	defer connA.Close() //nolint:errcheck

	connB, err := lisB.Accept()
	require.NoError(t, err)
	defer connB.Close() //nolint:errcheck

	payload := cipher.RandByte(2048)
	go func() { _, _ = connA.Write(payload) }() //nolint:errcheck
	got := make([]byte, len(payload))
	_, err = io.ReadFull(connB, got)
	require.NoError(t, err)
	require.Equal(t, payload, got, "payload mismatch over the TCP↔WS bridge through one unified port")

	require.NoError(t, clientA.Close())
	require.NoError(t, clientB.Close())
	require.NoError(t, srv.Close())
}
