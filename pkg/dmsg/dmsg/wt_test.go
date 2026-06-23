// Package dmsg pkg/dmsg/dmsg/wt_test.go
package dmsg

import (
	"context"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// TestWebTransportSession exercises the full dmsg-over-WebTransport path end to
// end: a server that listens ONLY over WebTransport (no TCP/QUIC/WS), two
// clients that dial it over WT (PreferWT) verifying the server's self-signed
// cert by pinned hash, and a stream bridged between them through the server.
// This proves the Noise handshake + yamux mux + server bridge all run unchanged
// over a single WebTransport bidirectional stream — i.e. WT is a drop-in
// transport, and the cert-hash pinning (the browser serverCertificateHashes
// model) authenticates the server with no CA.
//
// The server's discovery entry carries an empty TCP Address and only an
// AddressWT + CertHashWT, so there is no TCP fallback: a client reaching a
// session here MUST have done so over WebTransport.
func TestWebTransportSession(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("wt_server"))

	// UDP socket for the HTTP/3 (QUIC) WebTransport listener.
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpConn, err := net.ListenUDP("udp", udpAddr)
	require.NoError(t, err)

	cert, certHash, err := skyquic.NewWebTransportCertificate()
	require.NoError(t, err)

	wtURL := "https://" + udpConn.LocalAddr().String() + wtPath

	// Serve ONLY over WebTransport.
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.ServeWebTransport(udpConn, wtURL, cert, certHash) }()

	// Manually publish the server's discovery entry advertising only the WT
	// endpoint + cert hash (empty Address ⇒ no TCP fallback). ServeWebTransport
	// does not run the self-registration loop (that lives in Serve), so we post
	// it here.
	srvEntry := disc.NewServerEntry(pkSrv, 0, "", maxSessions)
	srvEntry.Server.AddressWT = wtURL
	srvEntry.Server.CertHashWT = hex.EncodeToString(certHash[:])
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	// Two WT-preferring clients.
	wtConf := func() *Config {
		c := DefaultConfig()
		c.Carriers = []string{CarrierWT}
		return c
	}
	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, wtConf())
	clientA.SetLogger(logging.MustGetLogger("wt_client_A"))
	go clientA.Serve(context.Background())

	pkB, skB := GenKeyPair(t, "client B")
	clientB := NewClient(pkB, skB, dc, wtConf())
	clientB.SetLogger(logging.MustGetLogger("wt_client_B"))
	go clientB.Serve(context.Background())

	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0 && clientB.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "clients failed to connect to DMSG server over WebTransport")

	// Both sessions must be to the WT server — confirms the WT dial succeeded
	// (there is no other server, and the entry carries no TCP Address).
	_, okA := clientA.Session(pkSrv)
	_, okB := clientB.Session(pkSrv)
	require.True(t, okA, "client A has no session to the WT server")
	require.True(t, okB, "client B has no session to the WT server")

	// Bridge a stream A→B through the server and round-trip a payload.
	const port = 8080
	lis, err := clientB.Listen(port)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	connA, err := clientA.DialStream(context.TODO(), Addr{PK: pkB, Port: port})
	require.NoError(t, err)
	defer connA.Close() //nolint:errcheck

	connB, err := lis.Accept()
	require.NoError(t, err)
	defer connB.Close() //nolint:errcheck

	payload := cipher.RandByte(4096)
	go func() {
		_, _ = connA.Write(payload) //nolint:errcheck
	}()

	got := make([]byte, len(payload))
	_, err = io.ReadFull(connB, got)
	require.NoError(t, err)
	require.Equal(t, payload, got, "payload mismatch over WebTransport-bridged stream")

	require.NoError(t, clientA.Close())
	require.NoError(t, clientB.Close())
	require.NoError(t, srv.Close())
}

// TestWebTransportCertHashMismatch verifies that a client which pins the WRONG
// cert hash is rejected at the TLS layer — the server-authentication guarantee
// that lets a browser safely dial a CA-free self-signed dmsg WebTransport
// endpoint.
func TestWebTransportCertHashMismatch(t *testing.T) {
	dc := disc.NewMock(0)
	const maxSessions = 10

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("wt_server_bad"))

	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpConn, err := net.ListenUDP("udp", udpAddr)
	require.NoError(t, err)

	cert, certHash, err := skyquic.NewWebTransportCertificate()
	require.NoError(t, err)
	wtURL := "https://" + udpConn.LocalAddr().String() + wtPath

	go func() { _ = srv.ServeWebTransport(udpConn, wtURL, cert, certHash) }() //nolint:errcheck

	// Advertise a DIFFERENT (wrong) hash than the cert the server actually serves.
	_, wrongHash, err := skyquic.NewWebTransportCertificate()
	require.NoError(t, err)
	srvEntry := disc.NewServerEntry(pkSrv, 0, "", maxSessions)
	srvEntry.Server.AddressWT = wtURL
	srvEntry.Server.CertHashWT = hex.EncodeToString(wrongHash[:])
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	pkA, skA := GenKeyPair(t, "client A")
	conf := DefaultConfig()
	conf.Carriers = []string{CarrierWT}
	clientA := NewClient(pkA, skA, dc, conf)
	clientA.SetLogger(logging.MustGetLogger("wt_client_bad"))
	go clientA.Serve(context.Background())

	// The session must NEVER come up: TLS verification rejects the mismatched
	// cert, and there is no TCP fallback (empty Address).
	require.Never(t, func() bool {
		return clientA.SessionCount() > 0
	}, 3*time.Second, 300*time.Millisecond, "client connected despite cert-hash mismatch")

	require.NoError(t, clientA.Close())
	require.NoError(t, srv.Close())
}
