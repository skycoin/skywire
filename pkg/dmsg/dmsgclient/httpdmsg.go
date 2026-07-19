// Package dmsgclient pkg/dmsg/dmsgclient/httpdmsg.go c1-net-dmsg
package dmsgclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// httpResult is a parsed minimal HTTP/1.1 response: the status code, the
// response headers (canonical-ish keys as sent on the wire), and the fully-read
// body.
type httpResult struct {
	status  int
	headers map[string]string
	body    []byte

	// reusable reports whether the stream may go back into the keep-alive
	// pool: the body was framed (Content-Length or chunked) so we stopped on
	// a clean message boundary, and the peer did not ask to close. A
	// read-to-EOF body is never reusable — the stream is spent by definition.
	reusable bool
}

// httpRoundTrip writes a minimal HTTP/1.1 request to conn and reads back the
// response. It is net/http-FREE — so it compiles under TinyGo, where net/http
// is broken on the js target — but speaks the exact HTTP/1.1 wire protocol, so
// it talks to the dmsg-served discovery (a standard net/http server reached over
// a dmsg stream) unchanged. It is the net/http-free equivalent of one
// dmsghttp.HTTPTransport round-trip.
//
// `Connection: keep-alive` is sent so the stream survives the response and can
// be reused for the next request. Pre-keep-alive this sent `Connection: close`,
// which made the server tear the stream down after EVERY request — so each
// periodic call (TPD registration, discovery poll) paid a full noise + PQ
// handshake on the server. That per-request handshake storm is what pinned
// dmsg-discovery's CPU: a production profile showed ~73% of ALL allocation in
// ClientSession.handshakeResponder (secp256k1 ECDH + ML-KEM), with GC burning
// ~2.6 cores while the actual request rate was modest. The net/http side was
// fixed the same way in dmsghttp.HTTPTransport; this is its net/http-free twin.
//
// br must be the reader that owns conn's buffered bytes — on a reused stream it
// carries whatever was already pulled off the wire.
//
// reqHeaders (may be nil) are extra request headers; Host, Connection and
// Content-Length are managed here and any same-named entries are ignored. A
// JSON Content-Type is assumed for a body only when reqHeaders sets none.
func httpRoundTrip(conn io.ReadWriter, br *bufio.Reader, method, host, path string, reqHeaders map[string]string, body []byte) (httpResult, error) {
	var req strings.Builder
	req.WriteString(method)
	req.WriteByte(' ')
	req.WriteString(path)
	req.WriteString(" HTTP/1.1\r\nHost: ")
	req.WriteString(host)
	req.WriteString("\r\nConnection: keep-alive\r\n")
	hasCType := false
	for k, v := range reqHeaders {
		switch strings.ToLower(k) {
		case "host", "connection", "content-length":
			continue // managed here
		case "content-type":
			hasCType = true
		}
		req.WriteString(k)
		req.WriteString(": ")
		req.WriteString(v)
		req.WriteString("\r\n")
	}
	if body != nil {
		if !hasCType {
			req.WriteString("Content-Type: application/json\r\n")
		}
		req.WriteString("Content-Length: ")
		req.WriteString(strconv.Itoa(len(body)))
		req.WriteString("\r\n")
	}
	req.WriteString("\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		return httpResult{}, err
	}
	if body != nil {
		if _, err := conn.Write(body); err != nil {
			return httpResult{}, err
		}
	}
	return readHTTPResponse(br)
}

// readHTTPResponse parses a minimal HTTP/1.1 response: status line, headers, and
// the body framed by Content-Length, chunked Transfer-Encoding, or read-to-EOF
// (the `Connection: close` case). br carries any bytes already buffered for this
// stream, so it is supplied by the caller rather than created here — a reused
// keep-alive stream may have the next response's opening bytes already read.
func readHTTPResponse(br *bufio.Reader) (httpResult, error) {
	statusLine, err := br.ReadString('\n')
	if err != nil {
		return httpResult{}, err
	}
	// "HTTP/1.1 200 OK\r\n" → fields[1] is the status code.
	fields := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	if len(fields) < 2 {
		return httpResult{}, fmt.Errorf("malformed HTTP status line: %q", statusLine)
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return httpResult{}, fmt.Errorf("malformed HTTP status code: %q", fields[1])
	}

	contentLength := -1
	chunked := false
	peerWantsClose := false
	headers := make(map[string]string)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return httpResult{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		val = strings.TrimSpace(val)
		headers[name] = val
		switch strings.ToLower(name) {
		case "content-length":
			if n, err := strconv.Atoi(val); err == nil {
				contentLength = n
			}
		case "transfer-encoding":
			if strings.Contains(strings.ToLower(val), "chunked") {
				chunked = true
			}
		case "connection":
			if strings.Contains(strings.ToLower(val), "close") {
				peerWantsClose = true
			}
		}
	}

	var bodyBytes []byte
	switch {
	case chunked:
		bodyBytes, err = readChunkedBody(br)
	case contentLength >= 0:
		bodyBytes = make([]byte, contentLength)
		_, err = io.ReadFull(br, bodyBytes)
	default:
		bodyBytes, err = io.ReadAll(br) // Connection: close → body ends at EOF
	}
	if err != nil {
		return httpResult{}, err
	}
	// Reusable only when the body was explicitly framed — then we stopped on a
	// clean message boundary and the next response starts where this one ended.
	// The read-to-EOF branch consumed the stream to its end, so it can never be
	// reused regardless of what the peer advertised.
	reusable := (chunked || contentLength >= 0) && !peerWantsClose
	return httpResult{status: code, headers: headers, body: bodyBytes, reusable: reusable}, nil
}

// FetchOverDmsg performs ONE HTTP/1.1 request over dmsg to pkHost (a public key,
// or "pk:port"; port defaults to the dmsg-HTTP port 80) and returns the response
// status, headers, and body — net/http-FREE, so it works under TinyGo. It is the
// transport the browser hypervisor UI uses to talk to a REMOTE visor/hypervisor
// by PK (the net/http-free analog of an http.Client over a dmsghttp transport).
func FetchOverDmsg(ctx context.Context, dmsgC *dmsg.Client, method, pkHost, path string, reqHeaders map[string]string, body []byte) (status int, respHeaders map[string]string, respBody []byte, err error) {
	var addr dmsg.Addr
	if err = addr.Set(pkHost); err != nil {
		return 0, nil, nil, fmt.Errorf("dmsgclient: bad fetch host %q: %w", pkHost, err)
	}
	if addr.Port == 0 {
		addr.Port = dmsg.DefaultDmsgHTTPPort
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Host header defaults to the destination PK, but a caller can override it
	// via reqHeaders["Host"] to carry a vhost — e.g. the resolving proxy reaching
	// a name-based virtual host (bunkerofdoom.com.<pk>.dmsg) served by a
	// vhost-aware backend (caddy/nginx) behind the visor. httpRoundTrip skips the
	// "host" key when emitting reqHeaders, so this is the only channel for it.
	hostHeader := addr.PK.Hex()
	if h := reqHeaders["Host"]; h != "" {
		hostHeader = h
	}

	res, err := fetchPooled(ctx, dmsgC, addr, method, hostHeader, path, reqHeaders, body)
	if err != nil {
		return 0, nil, nil, err
	}
	return res.status, res.headers, res.body, nil
}

// fetchPooled runs one HTTP/1.1 round-trip to addr over a keep-alive stream
// taken from dmsgC's pool, falling back to a fresh dial when none is idle.
//
// A pooled stream may be retired by the peer (or by its idle timeout) between
// our last use and this one, and we only discover that when the write or read
// fails. So a failure on a REUSED stream is retried once on a freshly-dialed
// one; a failure on a fresh stream is a real error and propagates. This is what
// makes keep-alive safe against servers that close early — including
// pre-v1.3.80 peers whose idle timeout is shorter than our pool's.
func fetchPooled(ctx context.Context, dmsgC *dmsg.Client, addr dmsg.Addr, method, hostHeader, path string, reqHeaders map[string]string, body []byte) (httpResult, error) {
	pool := poolFor(dmsgC)

	for attempt := 0; attempt < 2; attempt++ {
		// Only the first attempt may draw from the pool; the retry always dials
		// fresh. Otherwise a burst of streams that all went stale together (the
		// usual case — the peer restarted) would just hand us a second dead one.
		var ps *pooledStream
		reused := false
		if attempt == 0 {
			if ps = pool.get(addr); ps != nil {
				reused = true
			}
		}
		if ps == nil {
			stream, derr := dmsgC.DialStream(ctx, addr)
			if derr != nil {
				return httpResult{}, derr
			}
			ps = &pooledStream{s: stream, br: bufio.NewReader(stream)}
		}

		res, rerr := fetchOnce(ctx, ps, method, hostHeader, path, reqHeaders, body)
		if rerr != nil {
			ps.s.Close() //nolint:errcheck,gosec
			if reused {
				continue // stale pooled stream — retry on a fresh dial
			}
			return httpResult{}, rerr
		}

		if res.reusable {
			pool.put(addr, ps)
		} else {
			ps.s.Close() //nolint:errcheck,gosec
		}
		return res, nil
	}
	return httpResult{}, fmt.Errorf("dmsgclient: fetch %s failed after keep-alive retry", path)
}

// fetchOnce runs one request/response over ps, aborting the stream if ctx is
// canceled mid-flight.
func fetchOnce(ctx context.Context, ps *pooledStream, method, hostHeader, path string, reqHeaders map[string]string, body []byte) (httpResult, error) {
	done := make(chan struct{})
	defer close(done)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				ps.s.Close() //nolint:errcheck,gosec
			case <-done:
			}
		}()
	}
	return httpRoundTrip(ps.s, ps.br, method, hostHeader, path, reqHeaders, body)
}

// readChunkedBody decodes an HTTP/1.1 chunked body: a sequence of
// hex-size CRLF chunk-data CRLF, terminated by a zero-size chunk.
func readChunkedBody(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		sizeLine, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeLine = strings.TrimRight(sizeLine, "\r\n")
		if i := strings.IndexByte(sizeLine, ';'); i >= 0 {
			sizeLine = sizeLine[:i] // strip chunk extensions
		}
		size, err := strconv.ParseInt(strings.TrimSpace(sizeLine), 16, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed chunk size: %q", sizeLine)
		}
		if size == 0 {
			// Consume the trailer section up to the final blank line.
			for {
				line, err := br.ReadString('\n')
				if err != nil || strings.TrimRight(line, "\r\n") == "" {
					break
				}
			}
			return out, nil
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(br, chunk); err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if _, err := br.Discard(2); err != nil { // trailing CRLF after chunk data
			return nil, err
		}
	}
}
