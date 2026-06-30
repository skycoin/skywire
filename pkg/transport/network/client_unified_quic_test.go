//go:build !tinygo

package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestSharedQUICMux_ALPNDispatch is the core guarantee behind WT-on-transport_port:
// two QUIC application protocols with DIFFERENT, incompatible server tls.Configs
// (e.g. squicr's mutual-TLS vs WebTransport's no-client-auth) coexist on ONE
// quic.Transport / UDP socket, each connection routed to the handler registered
// for its negotiated ALPN. A regression here would silently break either squicr
// or WT whenever they share transport_port.
func TestSharedQUICMux_ALPNDispatch(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := newSharedQUICMux(conn, logging.MustGetLogger("test_shared_quic"))
	t.Cleanup(func() { _ = mux.Close(); _ = conn.Close() }) //nolint:errcheck

	const alpnA, alpnB = "proto-a", "proto-b"
	gotA := make(chan struct{}, 1)
	gotB := make(chan struct{}, 1)
	accept := func(sig chan struct{}) func(*quic.Conn) {
		return func(c *quic.Conn) {
			// Accept the dialer's stream so the dial completes, then signal.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.AcceptStream(ctx); err != nil {
				return
			}
			select {
			case sig <- struct{}{}:
			default:
			}
		}
	}
	require.NoError(t, mux.register(alpnA, serverTLSForALPN(t, alpnA), accept(gotA)))
	require.NoError(t, mux.register(alpnB, serverTLSForALPN(t, alpnB), accept(gotB)))

	// Dialing alpnA must reach handler A only; alpnB must reach handler B only.
	dialALPN(t, conn.LocalAddr(), alpnA)
	select {
	case <-gotA:
	case <-time.After(5 * time.Second):
		t.Fatal("alpnA dial did not reach handler A")
	}
	require.Len(t, gotB, 0, "alpnA dial must not reach handler B")

	dialALPN(t, conn.LocalAddr(), alpnB)
	select {
	case <-gotB:
	case <-time.After(5 * time.Second):
		t.Fatal("alpnB dial did not reach handler B")
	}
}

// TestSharedQUICMux_UnregisteredALPNRejected confirms a connection offering an
// ALPN no client has registered is refused at the handshake (getConfigForClient
// returns an error), rather than mis-dispatched.
func TestSharedQUICMux_UnregisteredALPNRejected(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := newSharedQUICMux(conn, logging.MustGetLogger("test_shared_quic"))
	t.Cleanup(func() { _ = mux.Close(); _ = conn.Close() }) //nolint:errcheck
	require.NoError(t, mux.register("known", serverTLSForALPN(t, "known"), func(*quic.Conn) {}))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tr := &quic.Transport{Conn: mustPacketConn(t)}
	t.Cleanup(func() { _ = tr.Close() })                                                                                              //nolint:errcheck
	_, derr := tr.Dial(ctx, conn.LocalAddr(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"unknown"}}, &quic.Config{}) //nolint:gosec
	require.Error(t, derr, "dial offering an unregistered ALPN must fail")
}

func serverTLSForALPN(t *testing.T, alpn string) *tls.Config {
	t.Helper()
	return &tls.Config{
		Certificates: []tls.Certificate{selfSignedCert(t)},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{alpn},
	}
}

func dialALPN(t *testing.T, addr net.Addr, alpn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tr := &quic.Transport{Conn: mustPacketConn(t)}
	t.Cleanup(func() { _ = tr.Close() })                                                                            //nolint:errcheck
	c, err := tr.Dial(ctx, addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{alpn}}, &quic.Config{}) //nolint:gosec
	require.NoError(t, err, "dial alpn %q", alpn)
	st, err := c.OpenStreamSync(ctx)
	require.NoError(t, err)
	_, _ = st.Write([]byte("hi")) //nolint:errcheck
}

func mustPacketConn(t *testing.T) net.PacketConn {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() }) //nolint:errcheck
	return pc
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}
