// Package network pkg/transport/network/quic_conn_test.go: in-process
// integration test of a real QUIC connection secured by the skywire-PK-
// bound TLS identity (option A). Proves the transport core — quic.Dial /
// quic.Listen + mutual PK authentication over a stream — works end to end
// before the address-resolver / transport-framework wiring.
package network

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func quicTestUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	return c
}

// TestQUICConnMutualPKAuth dials a real QUIC connection where the client
// pins the server's skywire PK and the server learns the client's — both
// via the option-A TLS binding — then round-trips bytes on a stream.
func TestQUICConnMutualPKAuth(t *testing.T) {
	skipQUICOnWindows(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	srvCert, err := newQUICCertificate(srvPK, srvSK)
	require.NoError(t, err)
	cliCert, err := newQUICCertificate(cliPK, cliSK)
	require.NoError(t, err)

	var mu sync.Mutex
	var gotClientPK cipher.PubKey
	srvTLS := quicTLSConfig(srvCert, nil, func(pk cipher.PubKey) {
		mu.Lock()
		gotClientPK = pk
		mu.Unlock()
	})
	cliTLS := quicTLSConfig(cliCert, &srvPK, nil) // client pins server PK

	qconf := &quic.Config{EnableDatagrams: true, HandshakeIdleTimeout: 5 * time.Second}

	srvUDP := quicTestUDP(t)
	defer srvUDP.Close() //nolint:errcheck
	lis, err := quic.Listen(srvUDP, srvTLS, qconf)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	srvDone := make(chan error, 1)
	go func() {
		conn, err := lis.Accept(context.Background())
		if err != nil {
			srvDone <- err
			return
		}
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			srvDone <- err
			return
		}
		buf := make([]byte, 5)
		if _, err := io.ReadFull(stream, buf); err != nil {
			srvDone <- err
			return
		}
		_, err = stream.Write(buf) // echo
		srvDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cliUDP := quicTestUDP(t)
	defer cliUDP.Close() //nolint:errcheck
	conn, err := quic.Dial(ctx, cliUDP, srvUDP.LocalAddr(), cliTLS, qconf)
	require.NoError(t, err)
	defer conn.CloseWithError(0, "done") //nolint:errcheck,gosec

	stream, err := conn.OpenStreamSync(ctx)
	require.NoError(t, err)
	_, err = stream.Write([]byte("hello"))
	require.NoError(t, err)
	buf := make([]byte, 5)
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(stream, buf)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(buf))

	require.NoError(t, <-srvDone)
	mu.Lock()
	assert.Equal(t, cliPK, gotClientPK, "server must learn the client's skywire PK")
	mu.Unlock()
}

// TestQUICDatagramRoundTrip proves the RFC 9221 datagram channel works over a
// real QUIC connection through the quicStreamConn wrapper — this is what makes
// faithful-UDP wire-real (no head-of-line blocking, unreliable delivery).
func TestQUICDatagramRoundTrip(t *testing.T) {
	skipQUICOnWindows(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()
	srvCert, err := newQUICCertificate(srvPK, srvSK)
	require.NoError(t, err)
	cliCert, err := newQUICCertificate(cliPK, cliSK)
	require.NoError(t, err)

	srvTLS := quicTLSConfig(srvCert, nil, nil)
	cliTLS := quicTLSConfig(cliCert, &srvPK, nil)
	qconf := &quic.Config{EnableDatagrams: true, HandshakeIdleTimeout: 5 * time.Second}

	srvUDP := quicTestUDP(t)
	defer srvUDP.Close() //nolint:errcheck
	lis, err := quic.Listen(srvUDP, srvTLS, qconf)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	srvConnCh := make(chan *quic.Conn, 1)
	go func() {
		conn, err := lis.Accept(context.Background())
		if err != nil {
			srvConnCh <- nil
			return
		}
		// Accept the stream the client opens to drive the handshake to completion.
		if _, err := conn.AcceptStream(context.Background()); err != nil {
			srvConnCh <- nil
			return
		}
		srvConnCh <- conn
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cliUDP := quicTestUDP(t)
	defer cliUDP.Close() //nolint:errcheck
	cliConn, err := quic.Dial(ctx, cliUDP, srvUDP.LocalAddr(), cliTLS, qconf)
	require.NoError(t, err)
	defer cliConn.CloseWithError(0, "done") //nolint:errcheck,gosec

	// Open + write a stream to force the handshake (datagrams require a
	// completed handshake).
	stream, err := cliConn.OpenStreamSync(ctx)
	require.NoError(t, err)
	_, err = stream.Write([]byte("x"))
	require.NoError(t, err)

	srvConn := <-srvConnCh
	require.NotNil(t, srvConn)
	defer srvConn.CloseWithError(0, "done") //nolint:errcheck,gosec

	// Datagram round-trip via the wrapper.
	cliWrap := &quicStreamConn{conn: cliConn}
	srvWrap := &quicStreamConn{conn: srvConn}

	payload := []byte("faithful-udp-over-quic")
	require.NoError(t, cliWrap.WriteDatagram(payload))
	got, err := srvWrap.ReadDatagram(ctx)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

// TestQUICConnRejectsWrongServerPK verifies the client-side pin: dialing a
// server while expecting a different PK fails the handshake.
func TestQUICConnRejectsWrongServerPK(t *testing.T) {
	skipQUICOnWindows(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()
	wrongPK, _ := cipher.GenerateKeyPair()

	srvCert, err := newQUICCertificate(srvPK, srvSK)
	require.NoError(t, err)
	cliCert, err := newQUICCertificate(cliPK, cliSK)
	require.NoError(t, err)

	srvTLS := quicTLSConfig(srvCert, nil, nil)
	cliTLS := quicTLSConfig(cliCert, &wrongPK, nil) // pin the WRONG server PK

	qconf := &quic.Config{HandshakeIdleTimeout: 3 * time.Second}

	srvUDP := quicTestUDP(t)
	defer srvUDP.Close() //nolint:errcheck
	lis, err := quic.Listen(srvUDP, srvTLS, qconf)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	go func() {
		// Accept may error when the client rejects the cert; ignore.
		if c, err := lis.Accept(context.Background()); err == nil {
			c.CloseWithError(0, "") //nolint:errcheck,gosec
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cliUDP := quicTestUDP(t)
	defer cliUDP.Close() //nolint:errcheck
	conn, err := quic.Dial(ctx, cliUDP, srvUDP.LocalAddr(), cliTLS, qconf)
	if err == nil {
		conn.CloseWithError(0, "") //nolint:errcheck,gosec
	}
	require.Error(t, err, "dialing with a wrong pinned server PK must fail")
}
