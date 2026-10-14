// Package visor pkg/visor/hostbridge_sockets_test.go c3-vis-wasm
package visor

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/hostbridge"
)

// testBridgeID is the visor the tests connect as, so the bridge stays paired
// with it across their reconnects.
var testBridgeID = func() *hostbridge.Pairing {
	pk, _ := cipher.GenerateKeyPair()
	return &hostbridge.Pairing{PK: pk.Hex()}
}()

func bridgeClient(t *testing.T, addr, token string) *hostbridge.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialHostBridgeConn(ctx, "ws://"+addr+"/"+token)
	require.NoError(t, err)
	c, err := hostbridge.NewClient(conn, testBridgeID)
	require.NoError(t, err)
	return c
}

func TestRebindAddr(t *testing.T) {
	bound := &net.TCPAddr{IP: net.IPv6unspecified, Port: 4242}
	require.Equal(t, ":4242", rebindAddr("", bound))
	require.Equal(t, ":4242", rebindAddr(":0", bound))
	require.Equal(t, "127.0.0.1:4242", rebindAddr("127.0.0.1:0", bound))
}

func TestHostBridgeSocketsSurviveReconnect(t *testing.T) {
	const token = "0123456789abcdef"
	srv, err := hostbridge.NewServer(hostbridge.Config{Token: token})
	require.NoError(t, err)
	bl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = bl.Close() }) //nolint:errcheck
	go srv.Serve(bl)                     //nolint:errcheck
	proxy := newCutProxy(t, bl.Addr().String())

	var s hostBridgeSockets
	_, err = s.Listen("tcp", "127.0.0.1:0")
	require.ErrorIs(t, err, errHostBridgeNotConnected)

	s.attach(bridgeClient(t, proxy.l.Addr().String(), token))
	l, err := s.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }() //nolint:errcheck
		}
	}()
	pc, err := s.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close() //nolint:errcheck
	host, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer host.Close() //nolint:errcheck

	udpRound := func(msg string) {
		t.Helper()
		got := make(chan string, 1)
		go func() {
			b := make([]byte, 100)
			_ = pc.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			n, _, err := pc.ReadFrom(b)
			if err == nil {
				got <- string(b[:n])
			}
			close(got)
		}()
		// A datagram can be lost while the bridge binds, so resend.
		for i := 0; i < 50; i++ {
			_, err := host.WriteTo([]byte(msg), pc.LocalAddr())
			require.NoError(t, err)
			select {
			case m := <-got:
				require.Equal(t, msg, m)
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		t.Fatal("datagram never arrived")
	}

	echoOnce(t, l.Addr().String(), "before the cut")
	udpRound("udp before")
	tcpAddr, udpAddr := l.Addr().String(), pc.LocalAddr().String()

	// Cut the session, then attach a new one, as the visor's supervisor does.
	proxy.cut()
	s.attach(nil)
	s.attach(bridgeClient(t, proxy.l.Addr().String(), token))

	require.Equal(t, tcpAddr, l.Addr().String())
	require.Equal(t, udpAddr, pc.LocalAddr().String())
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", tcpAddr, time.Second)
		if err != nil {
			return false
		}
		_ = c.Close() //nolint:errcheck
		return true
	}, 5*time.Second, 50*time.Millisecond)
	echoOnce(t, tcpAddr, "after the cut")
	udpRound("udp after")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := s.DialContext(ctx, "tcp", tcpAddr)
	require.NoError(t, err)
	_ = conn.Close() //nolint:errcheck
}

// A host address bound before the bridge connects, on a port another
// listener still holds, is listening once both clear.
func TestHostBridgeHostListenDeferred(t *testing.T) {
	const token = "0123456789abcdef"
	srv, err := hostbridge.NewServer(hostbridge.Config{Token: token})
	require.NoError(t, err)
	bl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = bl.Close() }) //nolint:errcheck
	go srv.Serve(bl)                     //nolint:errcheck

	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := held.Addr().String()

	var s hostBridgeSockets
	h := hostBridgeHost{&s}
	l, err := h.Listen("tcp", addr)
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck
	require.Equal(t, addr, l.Addr().String())
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }() //nolint:errcheck
		}
	}()

	_, err = h.DialTimeout("tcp", addr, time.Second)
	require.ErrorIs(t, err, errHostBridgeNotConnected)

	s.attach(bridgeClient(t, bl.Addr().String(), token))
	require.NoError(t, held.Close())
	require.Eventually(t, func() bool {
		c, err := h.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return false
		}
		defer c.Close()                                //nolint:errcheck
		_ = c.SetDeadline(time.Now().Add(time.Second)) //nolint:errcheck
		if _, err := c.Write([]byte("x")); err != nil {
			return false
		}
		b := make([]byte, 1)
		_, err = io.ReadFull(c, b)
		return err == nil && b[0] == 'x'
	}, 10*time.Second, 100*time.Millisecond)
	require.Equal(t, addr, l.Addr().String())
}
