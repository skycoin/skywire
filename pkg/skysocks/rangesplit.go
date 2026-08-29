// Package skysocks pkg/skysocks/rangesplit.go — transparent HTTP range-splitting.
//
// A browser or plain HTTP client pointed at the skysocks SOCKS5 proxy issues an
// ordinary GET. When the origin is reachable over plaintext HTTP (port 80) and
// advertises byte ranges (RFC 7233), the client fetches the body as several
// concurrent byte ranges over SEPARATE exit streams and reassembles it in order
// into a single 200 response — so one unmodified download spreads across the
// mesh's tunnels with no client cooperation. Anything that cannot be split (not
// a GET, an already-ranged request, a non-range origin, a small file) falls back
// to a byte-identical transparent splice.
//
// HTTPS (port 443) is NOT handled here: seeing the GET inside TLS requires
// terminating it at the proxy, and the resolver CA is name-constrained to
// .skynet/.dmsg/.skysocks (PermittedDNSDomainsCritical), so it cannot mint a
// browser-trusted leaf for a real origin. Transparent HTTPS range-splitting
// therefore needs a separate, unconstrained MITM root — a distinct security
// decision left as follow-up. HTTPS CONNECTs splice through unchanged.
package skysocks

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rangeSplitConfig tunes transparent HTTP range-splitting. Zero value is
// disabled; NewClient fills defaults and enables it.
type rangeSplitConfig struct {
	enabled     bool
	concurrency int   // number of concurrent range streams
	chunkSize   int64 // bytes per range request
}

const (
	defaultRSConcurrency = 8
	defaultRSChunkSize   = 4 << 20 // 4 MiB
	rsChunkRetries       = 3       // per-chunk redial attempts on churn
	rsHeadLimit          = 64 << 10
	rsProbeTimeout       = 20 * time.Second
	rsClassifyTimeout    = 5 * time.Second  // wait for the client's first request bytes
	rsHeadReadTimeout    = 10 * time.Second // finish reading the header block
)

func defaultRangeSplitConfig() rangeSplitConfig {
	return rangeSplitConfig{enabled: true, concurrency: defaultRSConcurrency, chunkSize: defaultRSChunkSize}
}

// isPort80 reports whether target ("host:port") is plaintext HTTP.
func isPort80(target string) bool {
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	return port == "80"
}

// serveHTTPRangeSplit takes ownership of a freshly-CONNECTed browser conn and the
// exit stream (stream0) already SOCKS5-CONNECTed to a :80 origin. It forwards the
// exit's CONNECT reply, peeks the browser's first HTTP request, and — when it is a
// splittable GET against a range-capable origin — fetches the body as N concurrent
// byte ranges over separate exit streams, reassembling it in order into a single
// synthesized 200 response. Anything not splittable falls back to a byte-identical
// transparent splice. It always closes conn and stream before returning.
func (c *Client) serveHTTPRangeSplit(conn, stream net.Conn) {
	host, doSplice, ok := c.rangeSplitInner(conn, stream)
	if ok {
		return
	}
	// Fall back to a transparent splice, replaying any bytes already read from the
	// browser so the exit sees an identical stream.
	c.splicePrefixed(conn, stream, doSplice)
	_ = host
}

// rangeSplitInner drives the split. It returns ok=true when it fully served the
// response (split or verbatim relay) and closed both ends. It returns ok=false to
// ask the caller to splice, handing back any browser bytes it had buffered so they
// can be replayed to the exit.
func (c *Client) rangeSplitInner(conn, stream net.Conn) (host string, clientPrefix []byte, ok bool) {
	// 1. Relay the exit's SOCKS5 CONNECT reply to the browser byte-for-byte so it
	//    proceeds to send its HTTP request.
	_ = stream.SetReadDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	reply, err := readSocks5Reply(stream)
	if err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return "", nil, true
	}
	if _, err := conn.Write(reply); err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return "", nil, true
	}
	clearDeadlines(conn, stream)

	// 2. Classify + read the browser's request head. Non-HTTP traffic on :80 (raw
	//    tunnels, server-speaks-first protocols) is detected the instant the bytes
	//    stop matching an HTTP method prefix and spliced with no meaningful delay.
	reqHead, isHTTP := peekRequestHead(conn, rsHeadLimit)
	clearDeadlines(conn)
	if !isHTTP {
		return "", reqHead, false // let caller splice, replaying what we read
	}
	req, perr := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqHead)))
	if perr != nil || !splittableRequest(req) {
		return "", reqHead, false
	}
	host = req.Host
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}

	// 3. chunk0 doubles as the range probe: original request + Range: bytes=0-(N-1).
	//    Only a Range request header is added; a non-range origin ignores it and
	//    returns its normal 200, so that fallback stays byte-identical.
	if _, err := stream.Write(injectRange(reqHead, 0, c.rs.chunkSize-1)); err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	br := bufio.NewReader(stream)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	if statusCode(statusLine) != 206 {
		// Origin ignored the Range (200) or returned a redirect/error. Relay it
		// verbatim: the added Range header does not appear in such responses, so the
		// browser sees exactly what its unmodified GET would have produced.
		_, _ = conn.Write([]byte(statusLine)) //nolint:errcheck
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)   //nolint:errcheck // peeking exactly Buffered() bytes never errors
			_, _ = conn.Write(b) //nolint:errcheck
		}
		_, _ = io.Copy(conn, stream) //nolint:errcheck
		conn.Close()                 //nolint:errcheck,gosec
		stream.Close()               //nolint:errcheck,gosec
		return host, nil, true
	}

	// 4. Parse 206 headers → total size + validator.
	tp := textproto.NewReader(br)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	total, okTotal := parseContentRangeTotal(hdr.Get("Content-Range"))
	if !okTotal || total <= 0 {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	validator := ifRangeValidator(hdr)

	// 5. Synthesized 200 header to the browser.
	if err := writeSynth200(conn, hdr, total); err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}

	// 6. chunk0 body (bytes 0..min(chunkSize,total)-1) straight from stream0.
	chunk0Len := c.rs.chunkSize
	if total < chunk0Len {
		chunk0Len = total
	}
	if _, err := io.CopyN(conn, br, chunk0Len); err != nil {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		return host, nil, true
	}
	stream.Close() //nolint:errcheck,gosec // stream0 done; remaining chunks use fresh streams
	if total <= c.rs.chunkSize {
		conn.Close() //nolint:errcheck,gosec
		return host, nil, true
	}

	// 7. Remaining chunks fetched concurrently over fresh exit streams, written in
	//    order. A failed chunk (after retries) truncates the download — the browser
	//    sees a short read and retries, exactly as with any dropped connection.
	if c.appCl != nil {
		c.appCl.Log().Debugf("range-split: %s %d bytes → %d chunks × %d streams",
			host, total, numChunks(total, c.rs.chunkSize), c.rs.concurrency)
	}
	// Observability counters (surfaced as proxystatus.RangeSplit): this is a
	// committed multi-chunk split, so record it and mark it in flight for the
	// duration of the concurrent fetch.
	c.rsSplits.Add(1)
	c.rsChunks.Add(uint64(numChunks(total, c.rs.chunkSize))) //nolint:gosec // numChunks>0 here (total>chunkSize)
	c.rsBytes.Add(uint64(total))                             //nolint:gosec // total>0 checked above
	c.rsActive.Add(1)
	c.streamRemainingChunks(conn, req, host, validator, total)
	c.rsActive.Add(-1)
	conn.Close() //nolint:errcheck,gosec
	return host, nil, true
}

// streamRemainingChunks fetches [chunkSize, total) as concurrent ranges and writes
// them to conn in order. Outstanding (in-flight + buffered) chunks are bounded to
// the configured concurrency so memory stays ~concurrency×chunkSize.
func (c *Client) streamRemainingChunks(conn net.Conn, req *http.Request, host, validator string, total int64) {
	type chunk struct {
		start, end int64
		buf        []byte
		err        error
		done       chan struct{}
	}
	var chunks []*chunk
	for start := c.rs.chunkSize; start < total; start += c.rs.chunkSize {
		end := start + c.rs.chunkSize - 1
		if end >= total {
			end = total - 1
		}
		chunks = append(chunks, &chunk{start: start, end: end, done: make(chan struct{})})
	}

	sem := make(chan struct{}, c.rs.concurrency)
	var wg sync.WaitGroup
	go func() {
		for _, ch := range chunks {
			sem <- struct{}{} // gate: at most `concurrency` chunks outstanding
			wg.Add(1)
			go func(ch *chunk) {
				defer wg.Done()
				ch.buf, ch.err = c.fetchChunkRetry(req, host, validator, ch.start, ch.end)
				close(ch.done)
			}(ch)
		}
	}()

	for _, ch := range chunks {
		<-ch.done
		if ch.err != nil {
			if c.appCl != nil {
				c.appCl.Log().Debugf("range-split: chunk %d-%d failed: %v", ch.start, ch.end, ch.err)
			}
			break // truncate; caller closes conn
		}
		if _, err := conn.Write(ch.buf); err != nil {
			break
		}
		ch.buf = nil
		<-sem // release one slot
	}
	// Drain any still-running fetches so their slots free and goroutines exit.
	go func() { wg.Wait() }()
}

// fetchChunkRetry fetches one byte range, redialing a fresh stream on failure.
func (c *Client) fetchChunkRetry(req *http.Request, host, validator string, start, end int64) ([]byte, error) {
	var err error
	for i := 0; i < rsChunkRetries; i++ {
		var buf []byte
		buf, err = c.fetchChunk(req, host, validator, start, end)
		if err == nil {
			return buf, nil
		}
	}
	return nil, err
}

// fetchChunk opens a new exit stream, SOCKS5-CONNECTs to host:80, issues a ranged
// GET carrying the original request's headers plus If-Range, and returns exactly
// the requested bytes.
func (c *Client) fetchChunk(req *http.Request, host, validator string, start, end int64) ([]byte, error) {
	sess := c.pickSession()
	if sess == nil {
		return nil, errAllTunnelsDown
	}
	st, err := sess.Open()
	if err != nil {
		return nil, err
	}
	defer st.Close() //nolint:errcheck,gosec

	_ = st.SetDeadline(time.Now().Add(rsProbeTimeout)) //nolint:errcheck
	if err := c.exitConnect(st, host, 80); err != nil {
		return nil, err
	}
	if _, err := st.Write(buildRangedGet(req, host, validator, start, end)); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("chunk %d-%d: status %d (validator changed?)", start, end, resp.StatusCode)
	}
	buf := make([]byte, end-start+1)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// exitConnect performs the SOCKS5 client handshake to the exit's proxy server and
// a CONNECT to host:port, with ATYP=domain so the EXIT resolves the name (matching
// how the browser's original request reached it).
func (c *Client) exitConnect(st net.Conn, host string, port int) error {
	if len(host) > 255 {
		return fmt.Errorf("host too long: %d", len(host))
	}
	if _, err := st.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(st, method); err != nil {
		return err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return fmt.Errorf("exit selected non-no-auth method %v", method)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // len(host)<=255 checked above
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port&0xff)) //nolint:gosec // port is 80, well within a byte pair
	if _, err := st.Write(req); err != nil {
		return err
	}
	_, err := readSocks5Reply(st)
	return err
}

// splicePrefixed is the original two-way splice, optionally replaying bytes already
// read from the browser to the exit first (so the exit sees an identical stream).
func (c *Client) splicePrefixed(conn, stream net.Conn, clientPrefix []byte) {
	const errorCount = 2
	errCh := make(chan error, errorCount)
	go func() {
		var src io.Reader = conn
		if len(clientPrefix) > 0 {
			src = io.MultiReader(bytes.NewReader(clientPrefix), conn)
		}
		_, err := io.Copy(stream, src)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()
	for i := 0; i < errorCount; i++ {
		if err := <-errCh; err != nil && c.appCl != nil {
			c.appCl.Log().Debugf("Copy error: %v", err)
		}
		if i == 0 {
			conn.Close()   //nolint:errcheck,gosec
			stream.Close() //nolint:errcheck,gosec
		}
	}
}

// --- small HTTP/SOCKS5 helpers ---

// readSocks5Reply reads one SOCKS5 reply (VER REP RSV ATYP ADDR PORT) and returns
// its raw bytes.
func readSocks5Reply(r io.Reader) ([]byte, error) {
	h := make([]byte, 4)
	if _, err := io.ReadFull(r, h); err != nil {
		return nil, err
	}
	if h[0] != 0x05 {
		return nil, fmt.Errorf("bad socks5 reply ver %d", h[0])
	}
	var addrLen int
	switch h[3] {
	case 0x01:
		addrLen = 4
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return nil, err
		}
		out := append(h, l...)
		rest := make([]byte, int(l[0])+2)
		if _, err := io.ReadFull(r, rest); err != nil {
			return nil, err
		}
		return append(out, rest...), nil
	case 0x04:
		addrLen = 16
	default:
		return nil, fmt.Errorf("bad socks5 atyp %d", h[3])
	}
	rest := make([]byte, addrLen+2)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	return append(h, rest...), nil
}

// httpMethodTokens are the request-method prefixes (with the trailing space) used
// to decide whether a :80 stream carries HTTP, without waiting for a full line.
var httpMethodTokens = []string{"GET ", "POST ", "PUT ", "HEAD ", "DELETE ", "OPTIONS ", "PATCH ", "TRACE ", "CONNECT "}

// classifyHTTP reports whether buf is the start of an HTTP request. decided=false
// means buf is still a strict prefix of some method token — read more before
// deciding. An empty buf is always undecided.
func classifyHTTP(buf []byte) (decided, isHTTP bool) {
	s := string(buf)
	prefixOfSome := false
	for _, m := range httpMethodTokens {
		if strings.HasPrefix(s, m) {
			return true, true
		}
		if len(s) < len(m) && strings.HasPrefix(m, s) {
			prefixOfSome = true
		}
	}
	if prefixOfSome {
		return false, false
	}
	return true, false
}

// peekRequestHead reads from conn just far enough to decide whether it carries an
// HTTP request and, if so, to capture the whole header block (up to limit). It
// returns isHTTP=false — with everything it read, for verbatim replay — the instant
// the bytes cannot be an HTTP method, when the head exceeds the cap, when extra
// bytes trail the header terminator (pipelining we won't split), or on any error.
func peekRequestHead(conn net.Conn, limit int) (head []byte, isHTTP bool) {
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)

	_ = conn.SetReadDeadline(time.Now().Add(rsClassifyTimeout)) //nolint:errcheck
	for {
		if decided, ok := classifyHTTP(buf); decided {
			if !ok {
				return buf, false
			}
			break
		}
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, false
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(rsHeadReadTimeout)) //nolint:errcheck
	term := []byte("\r\n\r\n")
	for !bytes.Contains(buf, term) {
		if len(buf) >= limit {
			return buf, false
		}
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, false
		}
	}
	if bytes.Index(buf, term)+4 < len(buf) {
		return buf, false // bytes trail the header block (pipelined) — do not split
	}
	return buf, true
}

// splittableRequest reports whether req is a plain GET we can range-split.
func splittableRequest(req *http.Request) bool {
	if req == nil || req.Method != http.MethodGet {
		return false
	}
	if req.ProtoMajor != 1 {
		return false
	}
	if req.Header.Get("Range") != "" {
		return false
	}
	if req.Header.Get("Upgrade") != "" {
		return false
	}
	return true
}

// injectRange inserts a single Range header before the blank line of an HTTP head.
func injectRange(head []byte, start, end int64) []byte {
	if !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
		return head
	}
	prefix := head[:len(head)-2] // drop the terminating blank line's CRLF
	line := fmt.Sprintf("Range: bytes=%d-%d\r\n\r\n", start, end)
	out := make([]byte, 0, len(prefix)+len(line))
	out = append(out, prefix...)
	out = append(out, line...)
	return out
}

// buildRangedGet reconstructs a GET carrying the original request's headers (minus
// hop-by-hop and range headers) plus Range, If-Range and Connection: close.
func buildRangedGet(req *http.Request, host, validator string, start, end int64) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", req.URL.RequestURI())
	fmt.Fprintf(&b, "Host: %s\r\n", req.Host)
	for k, vs := range req.Header {
		switch textproto.CanonicalMIMEHeaderKey(k) {
		case "Range", "Connection", "Proxy-Connection", "Keep-Alive", "If-Range", "Content-Length", "Transfer-Encoding":
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Range: bytes=%d-%d\r\n", start, end)
	if validator != "" {
		fmt.Fprintf(&b, "If-Range: %s\r\n", validator)
	}
	b.WriteString("Connection: close\r\n\r\n")
	_ = host
	return []byte(b.String())
}

// writeSynth200 writes a 200 response header derived from stream0's 206 headers,
// with the full Content-Length and Content-Range dropped.
func writeSynth200(conn net.Conn, hdr textproto.MIMEHeader, total int64) error {
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\r\n")
	for k, vs := range hdr {
		switch textproto.CanonicalMIMEHeaderKey(k) {
		case "Content-Range", "Content-Length", "Connection", "Keep-Alive", "Transfer-Encoding":
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", total)
	b.WriteString("Connection: close\r\n\r\n")
	_, err := conn.Write([]byte(b.String()))
	return err
}

// ifRangeValidator returns a strong ETag if present, else Last-Modified, for use
// as an If-Range value so a resource that changes mid-download aborts cleanly.
func ifRangeValidator(hdr textproto.MIMEHeader) string {
	if et := hdr.Get("Etag"); et != "" && !strings.HasPrefix(et, "W/") {
		return et
	}
	return hdr.Get("Last-Modified")
}

// statusCode parses the numeric code from an HTTP status line.
func statusCode(line string) int {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1]) //nolint:errcheck // a non-numeric status yields 0, handled by callers
	return n
}

// parseContentRangeTotal extracts the total size from "bytes start-end/total".
func parseContentRangeTotal(cr string) (int64, bool) {
	i := strings.LastIndex(cr, "/")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func numChunks(total, chunkSize int64) int64 {
	return (total + chunkSize - 1) / chunkSize
}
