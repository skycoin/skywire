//go:build !tinygo && !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/bridge_quic_idle_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// A stream through a server whose clients reach it over QUIC survives a
// response that takes longer than HandshakeTimeout. The server's bridge kept
// the request-read deadline on QUIC legs, so a CPU profile requested over
// dmsg was cut at 5 seconds.
func TestBridgeOverQUICOutlivesHandshakeTimeout(t *testing.T) {
	dc := disc.NewMock(0)
	pkSrv, skSrv := GenKeyPair(t, "idle_server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: 300 * time.Millisecond}, nil)
	srv.SetLogger(logging.MustGetLogger("idle_server"))
	tcpLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	wtURL := "https://" + udpConn.LocalAddr().String() + wtPath
	go func() { _ = srv.Serve(tcpLis, tcpLis.Addr().String()) }()                          //nolint:errcheck
	go func() { _ = srv.ServeUnifiedQUIC(udpConn, udpConn.LocalAddr().String(), wtURL) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })                                                  //nolint:errcheck
	require.Eventually(t, func() bool {
		e, eerr := dc.Entry(context.Background(), pkSrv)
		return eerr == nil && e.Server != nil && e.Server.AddressUDP != ""
	}, 10*time.Second, 50*time.Millisecond)

	newClient := func(name string) *Client {
		conf := DefaultConfig()
		conf.Carriers = []string{CarrierQUIC}
		pk, sk := GenKeyPair(t, name)
		c := NewClient(pk, sk, dc, conf)
		c.SetLogger(logging.MustGetLogger(name))
		go c.Serve(context.Background())
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		return c
	}
	a, b := newClient("idle_a"), newClient("idle_b")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range []*Client{a, b} {
		require.NoError(t, c.EnsureSession(ctx, entrySnapshot(t, dc, pkSrv)))
	}

	lis, err := b.Listen(4242)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck
	go func() {
		s, aerr := lis.AcceptStream()
		if aerr != nil {
			return
		}
		defer s.Close() //nolint:errcheck
		req := make([]byte, 3)
		if _, rerr := io.ReadFull(s, req); rerr != nil {
			return
		}
		time.Sleep(HandshakeTimeout + 2*time.Second)
		_, _ = s.Write([]byte("late")) //nolint:errcheck
	}()

	var str *Stream
	require.Eventually(t, func() bool {
		str, err = a.DialStream(ctx, Addr{PK: b.LocalPK(), Port: 4242})
		return err == nil
	}, 10*time.Second, 50*time.Millisecond)
	defer str.Close() //nolint:errcheck
	_, err = str.Write([]byte("req"))
	require.NoError(t, err)
	got := make([]byte, 4)
	_, err = io.ReadFull(str, got)
	require.NoError(t, err, "the bridge cut a response slower than HandshakeTimeout")
	require.Equal(t, "late", string(got))
}
