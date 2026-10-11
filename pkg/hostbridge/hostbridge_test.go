// Package hostbridge pkg/hostbridge/hostbridge_test.go c3-vis-wasm
package hostbridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

const testToken = "0123456789abcdef"

func startBridge(t *testing.T) string {
	t.Helper()
	s, err := NewServer(Config{Token: testToken, Origins: []string{DefaultOrigin}})
	require.NoError(t, err)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go s.Serve(l)                       //nolint:errcheck
	return l.Addr().String()
}

func dialWS(t *testing.T, url, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
}

func connect(t *testing.T, addr string) *Client {
	t.Helper()
	ws, _, err := dialWS(t, "ws://"+addr+"/"+testToken, DefaultOrigin)
	require.NoError(t, err)
	c, err := NewClient(websocket.NetConn(context.Background(), ws, websocket.MessageBinary))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	return c
}

func TestHandshakeRefusals(t *testing.T) {
	addr := startBridge(t)
	_, port, _ := net.SplitHostPort(addr) //nolint:errcheck
	cases := map[string]struct {
		url, origin string
		ok          bool
	}{
		"good":              {"ws://" + addr + "/" + testToken, DefaultOrigin, true},
		"no origin":         {"ws://" + addr + "/" + testToken, "", true},
		"localhost":         {"ws://localhost:" + port + "/" + testToken, DefaultOrigin, true},
		"wrong token":       {"ws://" + addr + "/nope", DefaultOrigin, false},
		"no token":          {"ws://" + addr + "/", DefaultOrigin, false},
		"other origin":      {"ws://" + addr + "/" + testToken, "https://evil.example", false},
		"rebound host name": {"ws://" + addr + "/" + testToken, DefaultOrigin, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if name == "rebound host name" {
				c, err := net.Dial("tcp", addr)
				require.NoError(t, err)
				defer c.Close() //nolint:errcheck
				_, err = io.WriteString(c, "GET /"+testToken+" HTTP/1.1\r\nHost: evil.example:"+port+
					"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
				require.NoError(t, err)
				b := make([]byte, 64)
				n, _ := c.Read(b) //nolint:errcheck
				require.True(t, strings.HasPrefix(string(b[:n]), "HTTP/1.1 403"), string(b[:n]))
				return
			}
			ws, resp, err := dialWS(t, tc.url, tc.origin)
			if tc.ok {
				require.NoError(t, err)
				_ = ws.CloseNow() //nolint:errcheck
				return
			}
			require.Error(t, err)
			require.NotNil(t, resp)
			require.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}

func echoServer(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }() //nolint:errcheck
		}
	}()
	return l
}

func TestDial(t *testing.T) {
	c := connect(t, startBridge(t))
	echo := echoServer(t)

	cn, err := c.Dial("tcp", echo.Addr().String())
	require.NoError(t, err)
	defer cn.Close() //nolint:errcheck
	require.Equal(t, echo.Addr().String(), cn.RemoteAddr().String())

	msg := strings.Repeat("skywire ", 50000)
	go func() { _, _ = io.WriteString(cn, msg) }() //nolint:errcheck
	got := make([]byte, len(msg))
	_, err = io.ReadFull(cn, got)
	require.NoError(t, err)
	require.Equal(t, msg, string(got))

	_, err = c.Dial("tcp", "127.0.0.1:1")
	require.Error(t, err)
	_, err = c.Dial("unix", "/tmp/x")
	require.Error(t, err)
}

func TestListen(t *testing.T) {
	c := connect(t, startBridge(t))
	l, err := c.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	host, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	defer host.Close() //nolint:errcheck

	in, err := l.Accept()
	require.NoError(t, err)
	require.Equal(t, host.LocalAddr().String(), in.RemoteAddr().String())
	require.Equal(t, l.Addr().String(), in.LocalAddr().String())

	_, err = io.WriteString(host, "ping")
	require.NoError(t, err)
	b := make([]byte, 4)
	_, err = io.ReadFull(in, b)
	require.NoError(t, err)
	require.Equal(t, "ping", string(b))

	// The tab closes its write side and the host still reads the reply first.
	_, err = io.WriteString(in, "pong")
	require.NoError(t, err)
	require.NoError(t, in.Close())
	reply, err := io.ReadAll(host)
	require.NoError(t, err)
	require.Equal(t, "pong", string(reply))

	require.NoError(t, l.Close())
	_, err = l.Accept()
	require.ErrorIs(t, err, net.ErrClosed)
	require.Eventually(t, func() bool {
		cn, err := net.Dial("tcp", l.Addr().String())
		if err == nil {
			_ = cn.Close() //nolint:errcheck
		}
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "host port still open after Close")
}

func TestListenPacket(t *testing.T) {
	c := connect(t, startBridge(t))
	pc, err := c.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close() //nolint:errcheck

	host, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer host.Close() //nolint:errcheck

	_, err = host.WriteTo([]byte("hello"), pc.LocalAddr())
	require.NoError(t, err)
	require.NoError(t, pc.SetReadDeadline(time.Now().Add(5*time.Second)))
	b := make([]byte, 1500)
	n, from, err := pc.ReadFrom(b)
	require.NoError(t, err)
	require.Equal(t, "hello", string(b[:n]))
	require.Equal(t, host.LocalAddr().String(), from.String())

	big := []byte(strings.Repeat("x", 8000))
	_, err = pc.WriteTo(big, host.LocalAddr())
	require.NoError(t, err)
	require.NoError(t, host.SetReadDeadline(time.Now().Add(5*time.Second)))
	n, from, err = host.ReadFrom(make([]byte, 9000))
	require.NoError(t, err)
	require.Equal(t, len(big), n)
	require.Equal(t, pc.LocalAddr().String(), from.String())

	// A datagram larger than the read buffer is truncated, and the next one
	// still arrives whole.
	_, err = host.WriteTo(big, pc.LocalAddr())
	require.NoError(t, err)
	_, err = host.WriteTo([]byte("next"), pc.LocalAddr())
	require.NoError(t, err)
	n, _, err = pc.ReadFrom(b[:100])
	require.NoError(t, err)
	require.Equal(t, 100, n)
	n, _, err = pc.ReadFrom(b)
	require.NoError(t, err)
	require.Equal(t, "next", string(b[:n]))
}

func TestSessionCloseFreesHostSockets(t *testing.T) {
	c := connect(t, startBridge(t))
	l, err := c.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, c.Close())
	_, err = l.Accept()
	require.ErrorIs(t, err, net.ErrClosed)
	require.Eventually(t, func() bool {
		cn, err := net.Dial("tcp", l.Addr().String())
		if err == nil {
			_ = cn.Close() //nolint:errcheck
		}
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "host port still open after the session ended")
}

func TestEmptyTokenRefused(t *testing.T) {
	_, err := NewServer(Config{})
	require.Error(t, err)
}
