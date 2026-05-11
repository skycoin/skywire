// Package skynetweb pkg/skynetweb/hostrewrite.go
//
// HTTP/1.1 Host-header rewriter for the resolver SOCKS5 dial path.
//
// # Motivation
//
// A visit to `https://example.com.<pk>.skynet/` resolves to the
// visor <pk>, but the browser sends `Host: example.com.<pk>.skynet`
// over the wire. When the backend is a vhost-capable HTTP server
// (Caddy, nginx, traefik) configured for `example.com`, that Host
// value doesn't match any of its sites and the request 404s.
//
// This wrapper sits between the SOCKS5 splice's plaintext stream
// (post-TLS-termination if TLS MITM is enabled) and the raw skywire
// conn to the backend. It parses each HTTP/1.1 request the browser
// sends, rewrites Host to the configured value (e.g. `example.com`),
// then re-emits the request to the backend. The response direction
// is passthrough — Set-Cookie/Location rewriting is left for a
// future iteration and the limitation is called out in the docs.
//
// Boundary: HTTP/1.1 only. h2c (plain HTTP/2) and SSE-with-binary-
// upgrade scenarios are not handled. Most reverse-proxy deployments
// terminate browser TLS upstream of the backend's plaintext face
// anyway, so HTTP/1.1 is the common case.
package skynetweb

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// hostRewriteConn wraps a net.Conn and rewrites the Host header on
// every HTTP/1.1 request flowing in the browser→backend direction.
// Response direction (backend→browser) is a straight passthrough.
//
// The wrapper exposes the io.ReadWriteCloser surface the SOCKS5
// splice expects:
//
//	SOCKS5.io.Copy(dst=hostRewriteConn, src=browser)    → Write side
//	SOCKS5.io.Copy(dst=browser, src=hostRewriteConn)    → Read side
//
// Write side: bytes from the browser (already TLS-decrypted by the
// MITMTerminate wrapper sitting "above" us in the stack) come in.
// We pipe them into an internal bufio.Reader that an async goroutine
// parses with http.ReadRequest, modifies, and re-emits to the
// backend with req.Write.
//
// Read side: bytes from the backend pass through unchanged.
type hostRewriteConn struct {
	backend    net.Conn
	targetHost string

	// Write-side pipe: bytes that arrive on Write are written to
	// pipeW; the parser goroutine reads from pipeR.
	pipeW *io.PipeWriter
	pipeR *io.PipeReader

	// Surface for closing the parser goroutine cleanly.
	closeOnce sync.Once
	doneCh    chan struct{}
}

// newHostRewriteConn wraps backend so writes are HTTP-parsed and
// have their Host header replaced with targetHost. targetHost must
// be non-empty; the caller (runtime.go) decides whether to apply
// the wrapper based on the parsed URL.
func newHostRewriteConn(backend net.Conn, targetHost string) *hostRewriteConn {
	if targetHost == "" {
		// Caller bug — guard rather than silently passing through.
		// A panic is preferable to silent breakage because this
		// path is only taken when a rewrite was explicitly chosen.
		panic("skynetweb: newHostRewriteConn called with empty targetHost")
	}
	pr, pw := io.Pipe()
	h := &hostRewriteConn{
		backend:    backend,
		targetHost: targetHost,
		pipeW:      pw,
		pipeR:      pr,
		doneCh:     make(chan struct{}),
	}
	go h.pump()
	return h
}

// pump runs on a dedicated goroutine. Reads HTTP requests from the
// internal pipe, rewrites Host, re-emits to backend. Exits when
// Close is called (pipe closure cascades into an EOF on Read).
func (h *hostRewriteConn) pump() {
	defer close(h.doneCh)
	defer h.backend.Close() //nolint:errcheck,gosec

	r := bufio.NewReader(h.pipeR)
	for {
		req, err := http.ReadRequest(r)
		if err != nil {
			// EOF or malformed input — we can't recover the stream.
			// Closing the backend conn lets the response pump on
			// the Read side notice and unblock. We don't have a
			// logger here, so any non-EOF parse error fails silently;
			// the connection close conveys the failure to the peer.
			return
		}

		// Rewrite Host. http.Request.Write uses req.Host (the canonical
		// field) for the wire-format Host: header; also clear any
		// stale "Host" entry in the header map so duplicates don't
		// slip through.
		req.Host = h.targetHost
		req.Header.Del("Host")

		// Clear RequestURI: required by req.Write which expects the
		// request to look "client-side" (URL.RequestURI() is used).
		req.RequestURI = ""

		// req.URL is parsed with the original Host. Reset Host on
		// URL too so any %-encoded reconstruction stays consistent.
		// Path/RawQuery come from RequestURI which we leave on req.URL.
		if req.URL != nil {
			req.URL.Host = h.targetHost
		}

		if err := req.Write(h.backend); err != nil {
			return
		}
	}
}

// Write accepts browser-side plaintext (post-TLS-MITM) and feeds it
// into the internal pipe for the parser goroutine.
func (h *hostRewriteConn) Write(p []byte) (int, error) {
	return h.pipeW.Write(p)
}

// Read passes backend bytes through unchanged.
func (h *hostRewriteConn) Read(p []byte) (int, error) {
	return h.backend.Read(p)
}

// Close tears down the pipe (causing the pump to exit) and closes
// the backend. Idempotent.
func (h *hostRewriteConn) Close() error {
	h.closeOnce.Do(func() {
		_ = h.pipeW.Close() //nolint:errcheck,gosec
		_ = h.pipeR.Close() //nolint:errcheck,gosec
	})
	// Backend close happens inside pump's defer; wait for it so the
	// caller's Close returns only after the goroutine has actually
	// released the backend.
	<-h.doneCh
	return nil
}

// net.Conn interface methods — delegate to backend for the address
// pair (so SOCKS5 sees a coherent local/remote pair) and accept
// deadlines on both directions.
func (h *hostRewriteConn) LocalAddr() net.Addr  { return h.backend.LocalAddr() }
func (h *hostRewriteConn) RemoteAddr() net.Addr { return h.backend.RemoteAddr() }
func (h *hostRewriteConn) SetDeadline(t time.Time) error {
	return h.backend.SetDeadline(t)
}
func (h *hostRewriteConn) SetReadDeadline(t time.Time) error {
	return h.backend.SetReadDeadline(t)
}
func (h *hostRewriteConn) SetWriteDeadline(t time.Time) error {
	return h.backend.SetWriteDeadline(t)
}

// splitHostSubdomain parses "<subdomain>.<pk>.<suffix>[:port]" or
// "<pk>.<suffix>[:port]" into pkLabel + subdomain + port.
//
//	"blog.<pk>.skynet"        → ("<pk>", "blog",        80)
//	"example.com.<pk>.skynet" → ("<pk>", "example.com", 80)
//	"<pk>.skynet"             → ("<pk>", "",            80)
//	"<pk>.skynet:1234"        → ("<pk>", "",          1234)
//
// suffix is expected to start with ".". Returns an error when the
// host shape doesn't match (no suffix match, no PK label, etc.) so
// the caller can fall back or surface a useful message.
func splitHostSubdomain(host, suffix string) (pkLabel, subdomain string, port uint16, err error) {
	// Split off optional port first.
	hostPart, portStr, hasPort := splitHostPort(host)
	port = 80
	if hasPort {
		p, perr := parseUint16(portStr)
		if perr != nil {
			return "", "", 0, fmt.Errorf("invalid port %q: %w", portStr, perr)
		}
		port = p
	}

	// hostPart must end in the suffix.
	if len(hostPart) <= len(suffix) || hostPart[len(hostPart)-len(suffix):] != suffix {
		return "", "", 0, fmt.Errorf("host %q has no suffix %q", hostPart, suffix)
	}
	stripped := hostPart[:len(hostPart)-len(suffix)]

	// The PK is the last label before the suffix. Anything before
	// it is the subdomain.
	lastDot := lastIndexByte(stripped, '.')
	if lastDot < 0 {
		// No subdomain — whole thing is the PK.
		return stripped, "", port, nil
	}
	pkLabel = stripped[lastDot+1:]
	subdomain = stripped[:lastDot]
	return pkLabel, subdomain, port, nil
}

// Small helpers kept inline so the parser doesn't pull in extra
// dependencies (strconv/strings) that the existing runtime.go
// already imports; using the std lib's equivalents from here would
// be the same code with import bloat.

func splitHostPort(host string) (h, p string, hasPort bool) {
	// Walk from the right; first ':' that isn't inside brackets
	// (IPv6) is the port separator. For our hostnames there are
	// no brackets.
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i], host[i+1:], true
		}
		if host[i] == ']' || host[i] == '/' {
			break
		}
	}
	return host, "", false
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func parseUint16(s string) (uint16, error) {
	if len(s) == 0 || len(s) > 5 {
		return 0, fmt.Errorf("port out of range")
	}
	var n uint32
	for _, ch := range []byte(s) {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-digit %q", ch)
		}
		n = n*10 + uint32(ch-'0')
		if n > 0xFFFF {
			return 0, fmt.Errorf("port out of range")
		}
	}
	return uint16(n), nil
}
