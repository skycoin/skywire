// Package dmsgclient pkg/dmsg/dmsgclient/httpdmsg.go
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
}

// httpRoundTrip writes a minimal HTTP/1.1 request to conn and reads back the
// response. It is net/http-FREE — so it compiles under TinyGo, where net/http
// is broken on the js target — but speaks the exact HTTP/1.1 wire protocol, so
// it talks to the dmsg-served discovery (a standard net/http server reached over
// a dmsg stream) unchanged. It is the net/http-free equivalent of one
// dmsghttp.HTTPTransport round-trip.
//
// `Connection: close` is sent so the server closes the stream after the
// response; the caller dials one fresh stream per request (no keep-alive reuse).
//
// reqHeaders (may be nil) are extra request headers; Host, Connection and
// Content-Length are managed here and any same-named entries are ignored. A
// JSON Content-Type is assumed for a body only when reqHeaders sets none.
func httpRoundTrip(conn io.ReadWriter, method, host, path string, reqHeaders map[string]string, body []byte) (httpResult, error) {
	var req strings.Builder
	req.WriteString(method)
	req.WriteByte(' ')
	req.WriteString(path)
	req.WriteString(" HTTP/1.1\r\nHost: ")
	req.WriteString(host)
	req.WriteString("\r\nConnection: close\r\n")
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
	return readHTTPResponse(conn)
}

// readHTTPResponse parses a minimal HTTP/1.1 response: status line, headers, and
// the body framed by Content-Length, chunked Transfer-Encoding, or read-to-EOF
// (the `Connection: close` case).
func readHTTPResponse(r io.Reader) (httpResult, error) {
	br := bufio.NewReader(r)

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
	return httpResult{status: code, headers: headers, body: bodyBytes}, nil
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

	stream, err := dmsgC.DialStream(ctx, addr)
	if err != nil {
		return 0, nil, nil, err
	}
	defer stream.Close() //nolint:errcheck

	done := make(chan struct{})
	defer close(done)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				stream.Close() //nolint:errcheck,gosec
			case <-done:
			}
		}()
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
	res, err := httpRoundTrip(stream, method, hostHeader, path, reqHeaders, body)
	if err != nil {
		return 0, nil, nil, err
	}
	return res.status, res.headers, res.body, nil
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
