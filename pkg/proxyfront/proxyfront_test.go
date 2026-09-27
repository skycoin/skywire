package proxyfront

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/armon/go-socks5"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"
)

// front runs one listener serving SOCKS5 and HTTP proxy requests. Every name
// resolves locally and every dial goes to target, so a reply proves the
// request went through the shared dial path.
func front(t *testing.T, target string) string {
	t.Helper()
	conf := &socks5.Config{
		Resolver: stubResolver{},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		},
	}
	srv, err := socks5.New(conf)
	require.NoError(t, err)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	sl := Split(l, func(c net.Conn) { ServeHTTP(ctx, c, SOCKSDial(conf.Resolver, conf.Dial)) })
	t.Cleanup(func() { cancel(); _ = sl.Close() }) //nolint:errcheck
	go srv.Serve(sl)                               //nolint:errcheck
	return l.Addr().String()
}

type stubResolver struct{}

func (stubResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, net.IPv4(127, 0, 0, 1), nil
}

// httpOrigin answers every request with its Host and path.
func httpOrigin(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", r.Host, r.URL.Path) //nolint:errcheck,gosec // test origin, plain text
	})}
	t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck
	go srv.Serve(l)                       //nolint:errcheck
	return l.Addr().String()
}

func TestSOCKS5StillServed(t *testing.T) {
	addr := front(t, httpOrigin(t))
	d, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	require.NoError(t, err)
	c := &http.Client{Transport: &http.Transport{DialContext: func(_ context.Context, n, a string) (net.Conn, error) { return d.Dial(n, a) }}}
	resp, err := c.Get("http://site.dmsg/socks")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "site.dmsg /socks", string(b))
}

func TestHTTPAbsoluteURL(t *testing.T) {
	addr := front(t, httpOrigin(t))
	pu, err := url.Parse("http://" + addr)
	require.NoError(t, err)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	for _, path := range []string{"/one", "/two"} {
		resp, err := c.Get("http://site.skynet" + path)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close() //nolint:errcheck
		require.Equal(t, "site.skynet "+path, string(b))
	}
}

func TestHTTPConnect(t *testing.T) {
	addr := front(t, httpOrigin(t))
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = io.WriteString(c, "CONNECT site.dmsg:80 HTTP/1.1\r\nHost: site.dmsg:80\r\n\r\n")
	require.NoError(t, err)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = io.WriteString(c, "GET /tunnel HTTP/1.1\r\nHost: site.dmsg\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	resp, err = http.ReadResponse(br, nil)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "site.dmsg /tunnel", string(b))
}

// TestHTTPConnectHalfClose: a client that sends, then half-closes, must still
// get the reply an origin writes only after seeing EOF.
func TestHTTPConnectHalfClose(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		b, _ := io.ReadAll(c)                                //nolint:errcheck // a short read still gets echoed
		_, _ = io.WriteString(c, strings.ToUpper(string(b))) //nolint:errcheck
		_ = c.Close()                                        //nolint:errcheck
	}()

	addr := front(t, l.Addr().String())
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = io.WriteString(c, "CONNECT echo.test:9 HTTP/1.1\r\nHost: echo.test:9\r\n\r\n")
	require.NoError(t, err)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = io.WriteString(c, "ping")
	require.NoError(t, err)
	require.NoError(t, c.(*net.TCPConn).CloseWrite())
	b, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Equal(t, "PING", string(b))
}

func TestHTTPDialFailureIs502(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	dead := l.Addr().String()
	require.NoError(t, l.Close())

	addr := front(t, dead)
	pu, err := url.Parse("http://" + addr)
	require.NoError(t, err)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	resp, err := c.Get("http://gone.dmsg/")
	require.NoError(t, err)
	_ = resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}
