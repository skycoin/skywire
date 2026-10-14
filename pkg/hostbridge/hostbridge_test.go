// Package hostbridge pkg/hostbridge/hostbridge_test.go c3-vis-wasm
package hostbridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

const testToken = "0123456789abcdef"

// testPairing is the visor every test connects as, so a bridge outside the
// test process stays paired with it.
var testPairing = newPairing()

func newPairing() *Pairing {
	pk, _ := cipher.GenerateKeyPair()
	return &Pairing{PK: pk.Hex()}
}

// startBridge runs a bridge in the test, or returns HOSTBRIDGE_EXTERNAL, the
// address of one started with -token 0123456789abcdef, such as a TinyGo build.
func startBridge(t *testing.T) string {
	t.Helper()
	if ext := os.Getenv("HOSTBRIDGE_EXTERNAL"); ext != "" {
		return ext
	}
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
	c, err := connectAs(t, addr, testPairing)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	return c
}

func connectAs(t *testing.T, addr string, p *Pairing) (*Client, error) {
	t.Helper()
	ws, _, err := dialWS(t, "ws://"+addr+"/"+testToken, DefaultOrigin)
	require.NoError(t, err)
	return NewClient(websocket.NetConn(context.Background(), ws, websocket.MessageBinary), p)
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

func TestResolveToken(t *testing.T) {
	tok, err := ResolveToken("given", "")
	require.NoError(t, err)
	require.Equal(t, "given", tok)

	path := filepath.Join(t.TempDir(), "sub", "token")
	first, err := ResolveToken("", path)
	require.NoError(t, err)
	require.Len(t, first, 32)
	again, err := ResolveToken("", path)
	require.NoError(t, err)
	require.Equal(t, first, again, "a restart must keep the token")

	a, err := ResolveToken("", "none")
	require.NoError(t, err)
	b, err := ResolveToken("", "none")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestDialContextCanceled(t *testing.T) {
	c := connect(t, startBridge(t))
	// 192.0.2.1 is TEST-NET-1 (RFC 5737), so the bridge's dial hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.DialContext(ctx, "tcp", "192.0.2.1:9")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)

	// The session still works afterwards.
	echo := echoServer(t)
	cn, err := c.DialContext(context.Background(), "tcp", echo.Addr().String())
	require.NoError(t, err)
	_ = cn.Close() //nolint:errcheck
}

func TestParseAddrPort(t *testing.T) {
	for in, want := range map[string]string{
		"[::]:4242":    "[::]:4242",
		"0.0.0.0:4242": "0.0.0.0:4242",
		"127.0.0.1:80": "127.0.0.1:80",
		":4242":        "0.0.0.0:4242",
	} {
		ap, err := parseAddrPort(in)
		require.NoError(t, err, in)
		require.Equal(t, want, ap.String())
	}
	for _, bad := range []string{"", "host:80", ":x", "nope"} {
		_, err := parseAddrPort(bad)
		require.Error(t, err, bad)
	}
}

func serveBridge(t *testing.T, cfg Config) string {
	t.Helper()
	s, err := NewServer(cfg)
	require.NoError(t, err)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go s.Serve(l)                       //nolint:errcheck
	return l.Addr().String()
}

func TestPairing(t *testing.T) {
	pairFile := filepath.Join(t.TempDir(), "hostbridge-visor")
	cfg := Config{Token: testToken, Origins: []string{DefaultOrigin}, PairFile: pairFile}
	first, second := newPairing(), newPairing()

	addr := serveBridge(t, cfg)
	c, err := connectAs(t, addr, first)
	require.NoError(t, err)
	_ = c.Close() //nolint:errcheck
	require.Len(t, first.Secret, 64, "pairing gives the visor a secret")

	// The first visor is recorded. Another one is refused, the first is not.
	b, err := os.ReadFile(pairFile) //nolint:gosec // a file in the test's temp dir
	require.NoError(t, err)
	require.Equal(t, first.PK+" "+first.Secret, strings.TrimSpace(string(b)))
	_, err = connectAs(t, addr, second)
	require.ErrorContains(t, err, first.PK)
	c, err = connectAs(t, addr, first)
	require.NoError(t, err)
	_ = c.Close() //nolint:errcheck

	// The right key with the wrong secret is refused, and so is asking to
	// pair again.
	_, err = connectAs(t, addr, &Pairing{PK: first.PK, Secret: strings.Repeat("0", 64)})
	require.ErrorContains(t, err, "wrong pairing secret")
	_, err = connectAs(t, addr, &Pairing{PK: first.PK})
	require.ErrorContains(t, err, "wrong pairing secret")

	// A restarted bridge reads the pairing back.
	addr = serveBridge(t, cfg)
	_, err = connectAs(t, addr, second)
	require.Error(t, err)
	c, err = connectAs(t, addr, first)
	require.NoError(t, err)
	_ = c.Close() //nolint:errcheck

	// Unpairing lets the next visor pair.
	require.NoError(t, Unpair(pairFile))
	addr = serveBridge(t, cfg)
	c, err = connectAs(t, addr, second)
	require.NoError(t, err)
	_ = c.Close() //nolint:errcheck
}

func TestHelloRefusesABadKey(t *testing.T) {
	addr := serveBridge(t, Config{Token: testToken, Origins: []string{DefaultOrigin}})
	_, err := connectAs(t, addr, &Pairing{PK: "not a key"})
	require.ErrorContains(t, err, "visor key")
}

func TestPairFile(t *testing.T) {
	require.Equal(t, "", PairFile("none"))
	require.Equal(t, filepath.Join("/x/y", "hostbridge-visor"), PairFile("/x/y/hostbridge-token"))
}
