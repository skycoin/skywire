// Package proxyfront pkg/proxyfront/proxyfront.go
//
// One listener, two proxy protocols. The resolving proxies speak SOCKS5; this
// lets the same port also answer HTTP proxy requests (CONNECT and absolute-URL
// requests), which is what an OS-level HTTP proxy setting — Android's
// VpnService.Builder.setHttpProxy, a browser's "HTTP proxy" field — speaks.
// A connection whose first byte is the SOCKS5 version goes to the SOCKS5
// server; anything else is handled as HTTP through the same dial path, so
// .dmsg, .skynet, the status pages and the upstream rules behave identically.
package proxyfront

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/armon/go-socks5"
)

// socks5Version is the first byte of every SOCKS5 client greeting.
const socks5Version = 0x05

// sniffTimeout bounds how long a new connection may stay silent before its
// protocol is known.
const sniffTimeout = 30 * time.Second

// DialFunc opens a connection to host:port the way the proxy's SOCKS5 server
// would for a CONNECT to that name.
type DialFunc func(ctx context.Context, host, port string) (net.Conn, error)

// Split wraps lis so that Accept returns only SOCKS5 connections; every other
// connection is handed to serveHTTP on its own goroutine.
func Split(lis net.Listener, serveHTTP func(net.Conn)) net.Listener {
	s := &splitListener{Listener: lis, socks: make(chan net.Conn), done: make(chan struct{})}
	go s.run(serveHTTP)
	return s
}

type splitListener struct {
	net.Listener
	socks chan net.Conn

	done chan struct{}
	err  error
}

func (s *splitListener) run(serveHTTP func(net.Conn)) {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			s.err = err
			close(s.done)
			return
		}
		go s.route(c, serveHTTP)
	}
}

// ServeConn applies the same protocol split to one already-accepted connection,
// for a caller with no listener to wrap: the resolvers' in-process local-service
// path, where a conn arrives over net.Pipe rather than from Accept. The conn is
// handed to serveSOCKS5 or serveHTTP, and closed here only when its first byte
// never arrives.
func ServeConn(c net.Conn, serveSOCKS5, serveHTTP func(net.Conn)) {
	pc, socks, ok := sniff(c)
	if !ok {
		return
	}
	if socks {
		serveSOCKS5(pc)
		return
	}
	serveHTTP(pc)
}

// sniff peeks the first byte to tell a SOCKS5 greeting from an HTTP request,
// returning the conn with that byte pushed back. ok is false when the peek
// failed, in which case the conn is already closed.
func sniff(c net.Conn) (pc net.Conn, socks, ok bool) {
	_ = c.SetReadDeadline(time.Now().Add(sniffTimeout)) //nolint:errcheck // unsupported on some conns
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{}) //nolint:errcheck
	if err != nil {
		_ = c.Close() //nolint:errcheck
		return nil, false, false
	}
	return &peekedConn{Conn: c, r: br}, first[0] == socks5Version, true
}

func (s *splitListener) route(c net.Conn, serveHTTP func(net.Conn)) {
	pc, socks, ok := sniff(c)
	if !ok {
		return
	}
	if !socks {
		serveHTTP(pc)
		return
	}
	select {
	case s.socks <- pc:
	case <-s.done:
		_ = c.Close() //nolint:errcheck
	}
}

// Accept returns the next SOCKS5 connection.
func (s *splitListener) Accept() (net.Conn, error) {
	select {
	case c := <-s.socks:
		return c, nil
	case <-s.done:
		if s.err != nil {
			return nil, s.err
		}
		return nil, net.ErrClosed
	}
}

// Close closes the underlying listener; run then sees the error, records it
// and ends Accept. Only run writes err, before closing done, so Accept reads
// it race-free.
func (s *splitListener) Close() error { return s.Listener.Close() }

// peekedConn replays the bytes read while sniffing.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// CloseWrite half-closes the underlying connection when it can.
func (c *peekedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}

// hopHeaders describe one transport hop and are not forwarded.
var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// ServeHTTP answers one HTTP proxy connection: a CONNECT becomes a tunnel,
// an absolute-URL request is forwarded and its response relayed. The
// connection is closed when it returns.
func ServeHTTP(ctx context.Context, c net.Conn, dial DialFunc) {
	defer c.Close() //nolint:errcheck
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		host, port := splitHostPort(req.Host, "443")
		up, err := dial(ctx, host, port)
		if err != nil {
			writeStatus(c, http.StatusBadGateway, err)
			return
		}
		defer up.Close() //nolint:errcheck
		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		splice(c, br, up)
		return
	}

	if req.URL.Host == "" || (req.URL.Scheme != "" && req.URL.Scheme != "http") {
		writeStatus(c, http.StatusBadRequest, errors.New("proxy requests need an absolute http:// URL, or CONNECT for https"))
		return
	}
	host, port := splitHostPort(req.URL.Host, "80")
	up, err := dial(ctx, host, port)
	if err != nil {
		writeStatus(c, http.StatusBadGateway, err)
		return
	}
	defer up.Close() //nolint:errcheck
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	// One request per upstream connection: a keep-alive client's next request
	// may name another host.
	req.Close = true
	if err := req.Write(up); err != nil {
		writeStatus(c, http.StatusBadGateway, err)
		return
	}
	_, _ = io.Copy(c, up) //nolint:errcheck
}

// splice copies both ways. Each direction's EOF is passed on as a half close,
// so a peer that finishes sending still receives the reply; it returns once
// both directions are done.
func splice(c net.Conn, br *bufio.Reader, up net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(up, br) //nolint:errcheck
		closeWrite(up)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(c, up) //nolint:errcheck
		closeWrite(c)
		done <- struct{}{}
	}()
	<-done
	<-done
}

// closeWrite half-closes c, or closes it outright when it cannot half close.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite() //nolint:errcheck
		return
	}
	_ = c.Close() //nolint:errcheck
}

func splitHostPort(hostport, def string) (string, string) {
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		return h, p
	}
	return strings.Trim(hostport, "[]"), def
}

func writeStatus(c net.Conn, code int, err error) {
	body := err.Error() + "\n"
	resp := &http.Response{
		StatusCode:    code,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
		Close:         true,
	}
	_ = resp.Write(c) //nolint:errcheck
}

// SOCKSDial builds a DialFunc from a SOCKS5 server's resolver and dial
// callback, taking the steps go-socks5 takes for a CONNECT: resolve a name
// (which is where the resolving proxies record the original hostname), then
// dial the resolved address.
func SOCKSDial(resolver socks5.NameResolver, dial func(ctx context.Context, network, addr string) (net.Conn, error)) DialFunc {
	return func(ctx context.Context, host, port string) (net.Conn, error) {
		addr := net.JoinHostPort(host, port)
		if net.ParseIP(host) == nil {
			rctx, ip, err := resolver.Resolve(ctx, host)
			if err != nil {
				return nil, err
			}
			ctx, addr = rctx, net.JoinHostPort(ip.String(), port)
		}
		return dial(ctx, "tcp", addr)
	}
}
