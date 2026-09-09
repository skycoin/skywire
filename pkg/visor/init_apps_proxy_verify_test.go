// Package visor pkg/visor/init_apps_proxy_verify_test.go c3-vis-core
package visor

import (
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/proxyinterstitial"
)

// TestRelayedResponseOK covers the verdict verifyProxyExit turns into
// "this exit works". The load-bearing case is the interstitial: with every
// tunnel down the local skysocks-client answers a plaintext-HTTP CONNECT
// itself with a perfectly ordinary-looking 200, so a check that only counts
// bytes certifies a dead exit.
func TestRelayedResponseOK(t *testing.T) {
	interstitial := readAllInterstitial(t)

	cases := map[string]struct {
		raw  string
		want bool
	}{
		"relayed 200":      {"HTTP/1.1 200 OK\r\nServer: nginx\r\n\r\n<html>hi</html>", true},
		"relayed 301":      {"HTTP/1.1 301 Moved Permanently\r\nLocation: /x\r\n\r\n", true},
		"relayed HTTP/1.0": {"HTTP/1.0 200 OK\r\n\r\nbody", true},
		// An origin's 4xx/5xx still travelled through the exit — that is relay.
		// neverssl.com answers a User-Agent-less request with 403; rejecting it
		// failed every working exit.
		"upstream 403":                     {"HTTP/1.1 403 Forbidden\r\n\r\n<html>", true},
		"upstream 404":                     {"HTTP/1.1 404 Not Found\r\n\r\n", true},
		"upstream 502":                     {"HTTP/1.1 502 Bad Gateway\r\n\r\n", true},
		"impossible code":                  {"HTTP/1.1 999 Nope\r\n\r\n", false},
		"not HTTP at all":                  {"garbage bytes from a zombie exit", false},
		"truncated status line":            {"HTTP/1.1 200 OK", false},
		"status line without a code":       {"HTTP/1.1\r\n\r\n", false},
		"non-numeric code":                 {"HTTP/1.1 OK OK\r\n\r\n", false},
		"locally synthesized interstitial": {interstitial, false},
		"older-build interstitial body": {
			"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n<body><div id=\"mesh-boot\">", false,
		},
	}
	for name, tc := range cases {
		if got := relayedResponseOK([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s: relayedResponseOK = %v, want %v", name, got, tc.want)
		}
	}
}

// readAllInterstitial renders the exact bytes the local skysocks-client writes
// back when it has no session to the exit.
func readAllInterstitial(t *testing.T) string {
	t.Helper()
	c := proxyinterstitial.Conn("neverssl.com", "", "skysocks", false)
	buf := make([]byte, 4096)
	n, _ := c.Read(buf) //nolint:errcheck
	if n == 0 {
		t.Fatal("interstitial rendered no bytes")
	}
	return string(buf[:n])
}

// TestRelayedResponseOKRejectsLiveInterstitial drives the REAL wire path the
// bug ran through: a SOCKS5 client CONNECTs to a plaintext-HTTP target, the
// local skysocks-client answers it in-process because it has no session to the
// exit (proxyinterstitial.ServeSOCKS5), and the reply is read back exactly as
// verifyProxyExit reads it. The verdict must be "not relayed".
func TestRelayedResponseOKRejectsLiveInterstitial(t *testing.T) {
	for _, tc := range []struct {
		name          string
		exitReachable func() bool
	}{
		{"no session at all", nil},
		{"transient failure, reload fall-through", func() bool { return true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln := newPipeListener()
			defer ln.Close() //nolint:errcheck
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					go func(c net.Conn) {
						_ = proxyinterstitial.ServeSOCKS5(c, "", "skysocks", nil, tc.exitReachable) //nolint:errcheck
						_ = c.Close()                                                               //nolint:errcheck
					}(c)
				}
			}()

			sd, err := proxy.SOCKS5("tcp", "socks.invalid:1080", nil, ln)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := sd.Dial("tcp", "neverssl.com:80")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()                                                                             //nolint:errcheck
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))                                          //nolint:errcheck
			_, _ = conn.Write([]byte("GET / HTTP/1.0\r\nHost: neverssl.com\r\nConnection: close\r\n\r\n")) //nolint:errcheck
			head := make([]byte, proxyVerifyReadLimit)
			n, _ := io.ReadFull(conn, head) //nolint:errcheck
			if n == 0 {
				t.Fatal("no response from the interstitial path")
			}
			if relayedResponseOK(head[:n]) {
				t.Errorf("locally synthesized response was accepted as a relayed reply:\n%q", head[:n])
			}
		})
	}
}

// pipeListener is a net.Listener whose Dial hands back the client end of an
// in-memory pipe, so it can also serve as the proxy.Dialer that reaches it.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Dial(_, _ string) (net.Conn, error) {
	cli, srv := net.Pipe()
	select {
	case l.conns <- srv:
		return cli, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// TestProxyExitTakenOver covers the decision that stops the auto-exit loop from
// stopping or re-pointing a skysocks-client the operator started. The observed
// failure was `cli proxy start <pk>` losing its exit to a random discovery pick
// because the loop treated the operator's session as its own failed candidate.
func TestProxyExitTakenOver(t *testing.T) {
	const loopPK = "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36"
	const operatorPK = "038af6b913d13eb726d8d1e195968af0cb894f2a0316fe98a031135e3c102da165"
	cases := []struct {
		name          string
		configured    string
		want          string
		running       bool
		expectRunning bool
		takenOver     bool
	}{
		{"loop's own exit still configured", loopPK, loopPK, true, true, false},
		{"operator re-pointed the client", operatorPK, loopPK, true, true, true},
		{"operator re-pointed it and it already died", operatorPK, loopPK, false, true, true},
		{"operator started a client the loop had stopped", operatorPK, "", true, false, true},
		{"nothing running where the loop expects nothing", operatorPK, "", false, false, false},
		{"pinned exit unchanged and down", loopPK, loopPK, false, true, false},
		{"srv cleared entirely is not a takeover", "", loopPK, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyExitTakenOver(tc.configured, tc.want, tc.running, tc.expectRunning); got != tc.takenOver {
				t.Fatalf("proxyExitTakenOver(%q, %q, running=%v, expectRunning=%v) = %v, want %v",
					tc.configured, tc.want, tc.running, tc.expectRunning, got, tc.takenOver)
			}
		})
	}
}
