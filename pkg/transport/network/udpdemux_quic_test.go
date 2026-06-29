//go:build !tinygo

package network

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestUDPDemux_QUICRealTraffic validates the demux against REAL quic-go traffic
// (not synthetic packets): a quic-go listener runs over the demux's QUIC virtual
// conn on a shared socket, a quic-go dialer connects to the shared socket's
// address, and the demux must classify+route the actual QUIC handshake so the
// session establishes and a stream carries bytes both ways. This is the de-risk
// for wiring the demux under the production QUIC transport.
func TestUDPDemux_QUICRealTraffic(t *testing.T) {
	skipQUICOnWindows(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()
	srvCert, err := newQUICCertificate(srvPK, srvSK)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	cliCert, err := newQUICCertificate(cliPK, cliSK)
	if err != nil {
		t.Fatalf("client cert: %v", err)
	}

	master, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("master socket: %v", err)
	}
	d := newUDPDemux(master)
	defer d.Close() //nolint:errcheck

	qconf := &quic.Config{EnableDatagrams: true}
	lis, err := quic.Listen(d.Conn(protoQUIC), quicTLSConfig(srvCert, nil, nil), qconf)
	if err != nil {
		t.Fatalf("quic.Listen over demux: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	srvErr := make(chan error, 1)
	go func() {
		conn, err := lis.Accept(ctx)
		if err != nil {
			srvErr <- err
			return
		}
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			srvErr <- err
			return
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(stream, buf); err != nil {
			srvErr <- err
			return
		}
		_, err = stream.Write(buf) // echo
		srvErr <- err
	}()

	dialUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	defer dialUDP.Close() //nolint:errcheck

	qc, err := quic.Dial(ctx, dialUDP, master.LocalAddr(), quicTLSConfig(cliCert, &srvPK, nil), qconf)
	if err != nil {
		t.Fatalf("quic.Dial to the shared socket (demux must route the handshake to QUIC): %v", err)
	}
	stream, err := qc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	buf := make([]byte, 4)
	if err := readStreamFull(stream, buf); err != nil {
		t.Fatalf("client read echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", buf)
	}
	if err := <-srvErr; err != nil {
		t.Fatalf("server side: %v", err)
	}
}

func readStreamFull(s *quic.Stream, b []byte) error {
	_ = s.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, err := io.ReadFull(s, b)
	return err
}
