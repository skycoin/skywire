// Package skynetweb pkg/skynetweb/publish_test.go
package skynetweb

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/logging"
)

// pipeForward hands an already-connected conn to a SOCKS5 client that expects to
// dial its proxy. It stands in for the app-plane conn an app would get from a
// dial to its own visor.
type pipeForward struct{ conn net.Conn }

func (p pipeForward) Dial(_, _ string) (net.Conn, error) { return p.conn, nil }

// publishedServe starts a resolver whose only reachable face is the published
// local-service handler, and returns it.
func publishedServe(t *testing.T, body string) func(net.Conn) {
	t.Helper()
	dialer := fakeDialer{
		onDial: func() (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()                                        //nolint:errcheck,gosec
				_ = server.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck,gosec
				buf := make([]byte, 4096)
				_, _ = server.Read(buf) //nolint:errcheck,gosec
				reply := "HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len(body)) +
					"\r\nConnection: close\r\n\r\n" + body
				_, _ = server.Write([]byte(reply)) //nolint:errcheck,gosec
			}()
			return client, nil
		},
	}

	published := make(chan func(net.Conn), 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = Run(ctx, logging.MustGetLogger("skynetweb-publish-test"), dialer, Config{ //nolint:errcheck,gosec
			ProxyPort: pickFreePort(t),
			Publish:   func(serve func(net.Conn)) { published <- serve },
		})
	}()

	select {
	case serve := <-published:
		return serve
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime never published a local-service handler")
		return nil
	}
}

// The published handler answers a SOCKS5 CONNECT exactly as the listener does,
// on a conn that never came from a listener: this is how an app on the visor
// reaches the resolver over its own data plane.
func TestPublishedHandlerServesSOCKS5(t *testing.T) {
	serve := publishedServe(t, "published!")

	appEnd, svcEnd := net.Pipe()
	go serve(svcEnd)

	socks, err := proxy.SOCKS5("tcp", "127.0.0.1:1", nil, pipeForward{appEnd})
	if err != nil {
		t.Fatalf("SOCKS5 client: %v", err)
	}
	host := testPK + ".skynet"
	conn, err := socks.Dial("tcp", host+":80")
	if err != nil {
		t.Fatalf("SOCKS5 dial over the published handler: %v", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := io.ReadAll(conn) //nolint:errcheck,gosec
	if !strings.Contains(string(got), "published!") {
		t.Errorf("body = %q", string(got))
	}
}

// The same handler also answers an HTTP-proxy request, because it runs the same
// protocol sniff the listener does.
func TestPublishedHandlerServesHTTPProxy(t *testing.T) {
	serve := publishedServe(t, "http-proxied!")

	appEnd, svcEnd := net.Pipe()
	go serve(svcEnd)
	defer appEnd.Close() //nolint:errcheck,gosec

	host := testPK + ".skynet"
	if err := appEnd.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	req := "GET http://" + host + "/ HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
	if _, err := appEnd.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := io.ReadAll(appEnd) //nolint:errcheck,gosec
	if !strings.Contains(string(got), "http-proxied!") {
		t.Errorf("body = %q", string(got))
	}
}
