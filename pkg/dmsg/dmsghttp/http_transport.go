// Package dmsghttp pkg/dmsghttp/http_transport.go
package dmsghttp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

const (
	defaultHTTPPort = uint16(80)

	// poolMaxIdlePerHost bounds the number of idle dmsg streams we keep
	// per destination. Six service hosts × 4 idle each = at most ~24
	// idle streams under realistic visor load — plenty of headroom for
	// the per-90-s heartbeat cadence without holding too many ports.
	poolMaxIdlePerHost = 4

	// poolIdleTimeout sits below dmsg.StreamIdleTimeout (2 min) so we
	// always evict before the underlying stream's read deadline fires
	// and tears the connection down out from under us.
	poolIdleTimeout = 90 * time.Second
)

// HTTPTransport implements http.RoundTripper. It dials fresh dmsg
// streams via dmsgC.DialStream, runs HTTP/1.1 request/response over
// the stream directly, and pools idle streams per destination so
// follow-up requests skip the noise handshake.
//
// Pre-pool every HTTP request opened a brand-new dmsg stream and paid
// a full noise handshake — every visor heartbeat, every TPD/AR/SD
// poll. With ~50 visor heartbeats/sec across the network this drove
// dmsg-server CPU into the ground (production pprof showed
// handshakeResponder + secp256k1 ops at ~50 % cum on dmsg-server-1).
// Pooling collapses sustained per-host traffic onto a small number
// of long-lived streams.
//
// We don't use the standard net/http.Transport with a custom
// DialContext because dmsg.Stream.Read auto-extends the read deadline
// to StreamIdleTimeout (a porter-cleanup safety net) on every
// successful read, which fights net/http's idle-conn detection in
// ways that produce flaky concurrent behavior under load. The pool
// here is small, focused, and uses one stream per in-flight request.
//
// Do not confuse with a Skywire Transport implementation.
type HTTPTransport struct {
	dmsgC *dmsg.Client

	mu       sync.Mutex
	idle     map[dmsg.Addr][]*pooledStream
	closed   bool
	tracking map[*pooledStream]struct{}
}

// pooledStream wraps a dmsg.Stream with the timestamp it became idle.
type pooledStream struct {
	s         *dmsg.Stream
	br        *bufio.Reader
	host      dmsg.Addr
	idleAt    time.Time
	closeOnce sync.Once
}

// MakeHTTPTransport returns an HTTPTransport with a per-destination
// stream pool. The ctx parameter is retained for API compatibility
// with pre-pool callers; pool lifecycle is driven by dmsgC.
//
// Returns a pointer because the transport carries pool state that
// must be shared across RoundTrip calls. Callers that hold the
// returned value will share one pool — copying it via assignment
// silently divides the pool, which is why we return *HTTPTransport
// rather than HTTPTransport.
func MakeHTTPTransport(_ context.Context, dmsgC *dmsg.Client) *HTTPTransport {
	return &HTTPTransport{
		dmsgC:    dmsgC,
		idle:     make(map[dmsg.Addr][]*pooledStream),
		tracking: make(map[*pooledStream]struct{}),
	}
}

// RoundTrip implements http.RoundTripper. Reuses an idle stream to the
// destination if one is available; otherwise dials a fresh one. After
// the response body is fully read and closed, the stream goes back
// into the idle pool. On any I/O error the stream is discarded.
func (t *HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Tag the request context so dmsgfirst's fallbackClient knows it's
	// running inside a dmsghttp dial and must not re-enter this same
	// transport for its own discovery lookups (would recurse until the
	// goroutine stack blows). See recursion_guard.go.
	req = req.WithContext(WithRecursionGuard(req.Context()))
	if req.URL.Scheme == "dmsg" {
		req = req.Clone(req.Context())
		req.URL.Scheme = "http"
	}

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	var hostAddr dmsg.Addr
	if err := hostAddr.Set(host); err != nil {
		return nil, fmt.Errorf("invalid host address: %w", err)
	}
	if hostAddr.Port == 0 {
		hostAddr.Port = defaultHTTPPort
	}

	// Try one pooled stream first; if the write or response read fails
	// (idle conn was closed by peer between requests), fall through to
	// a fresh dial. We never retry idempotent requests beyond a single
	// pooled-then-fresh attempt because the test/production cost of
	// multi-retry is higher than just paying a fresh handshake.
	if ps := t.takeIdle(hostAddr); ps != nil {
		resp, err := t.do(ps, req)
		if err == nil {
			return resp, nil
		}
		ps.discard()
		t.untrack(ps)
		// req.Write on the dead pooled stream consumed req.Body. Without
		// rewinding it the fresh-dial attempt below would send 0 body
		// bytes against the original ContentLength, producing
		// "http: ContentLength=N with Body length 0". Mirror
		// net/http.Transport's retry behavior: rewind via GetBody when
		// available. POSTs created with http.NewRequest(*bytes.Buffer
		// |*bytes.Reader|*strings.Reader) get GetBody set automatically.
		if err := rewindBody(req); err != nil {
			return nil, err
		}
		// fall through to fresh dial
	}

	stream, err := t.dmsgC.DialStream(req.Context(), hostAddr)
	if err != nil {
		return nil, err
	}
	ps := &pooledStream{s: stream, br: bufio.NewReader(stream), host: hostAddr}
	t.track(ps)
	resp, err := t.do(ps, req)
	if err != nil {
		ps.discard()
		t.untrack(ps)
		return nil, err
	}
	return resp, nil
}

// rewindBody resets req.Body to a fresh reader using req.GetBody, so a
// retry can re-read the body after a failed write attempt. Returns an
// error only if GetBody is set but produces an error; if GetBody is nil
// (e.g. body is nil/NoBody or non-rewindable) the request is left
// unchanged and the retry sends no body — which is fine for the only
// requests that hit this path with no GetBody (auth nonce GETs etc).
func rewindBody(req *http.Request) error {
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = body
	return nil
}

// do writes req to ps and reads back the response. The response body
// is wrapped so that closing it returns the stream to the idle pool
// (or discards it on error).
func (t *HTTPTransport) do(ps *pooledStream, req *http.Request) (*http.Response, error) {
	if err := req.Write(ps.s); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(ps.br, req)
	if err != nil {
		return nil, err
	}
	resp.Body = &pooledBody{
		ReadCloser: resp.Body,
		ps:         ps,
		t:          t,
		// HTTP/1.1 servers signal "no keep-alive" via Connection: close;
		// honor it by not returning the stream to the pool.
		keepAlive: !req.Close && !resp.Close && resp.ProtoAtLeast(1, 1),
	}
	return resp, nil
}

// takeIdle pops an idle stream for host if one exists, evicting any
// that have been idle longer than poolIdleTimeout.
func (t *HTTPTransport) takeIdle(host dmsg.Addr) *pooledStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	queue := t.idle[host]
	now := time.Now()
	for len(queue) > 0 {
		ps := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if now.Sub(ps.idleAt) > poolIdleTimeout {
			ps.discard()
			delete(t.tracking, ps)
			continue
		}
		t.idle[host] = queue
		return ps
	}
	delete(t.idle, host)
	return nil
}

// putIdle places a stream back into the idle pool, evicting the
// oldest entry if the per-host capacity is full.
func (t *HTTPTransport) putIdle(ps *pooledStream) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		ps.discard()
		return
	}
	queue := t.idle[ps.host]
	if len(queue) >= poolMaxIdlePerHost {
		// Evict the oldest (front of slice) to make room.
		oldest := queue[0]
		queue = queue[1:]
		oldest.discard()
		delete(t.tracking, oldest)
	}
	ps.idleAt = time.Now()
	queue = append(queue, ps)
	t.idle[ps.host] = queue
	t.mu.Unlock()
}

func (t *HTTPTransport) track(ps *pooledStream) {
	t.mu.Lock()
	t.tracking[ps] = struct{}{}
	t.mu.Unlock()
}

func (t *HTTPTransport) untrack(ps *pooledStream) {
	t.mu.Lock()
	delete(t.tracking, ps)
	t.mu.Unlock()
}

// CloseIdleConnections closes every idle stream the transport
// currently holds. Useful for tests and for callers that want to
// force a clean restart of connectivity (e.g. after a dmsg session
// reset). Pre-existing in-flight requests on streams that are not
// yet idle are unaffected.
func (t *HTTPTransport) CloseIdleConnections() {
	t.mu.Lock()
	hosts := t.idle
	t.idle = make(map[dmsg.Addr][]*pooledStream)
	t.mu.Unlock()
	for _, queue := range hosts {
		for _, ps := range queue {
			ps.discard()
			t.untrack(ps)
		}
	}
}

func (ps *pooledStream) discard() {
	ps.closeOnce.Do(func() {
		_ = ps.s.Close() //nolint:errcheck
	})
}

// pooledBody wraps the response body so that closing it (after the
// caller has read the body) returns the underlying stream to the
// idle pool when keep-alive is in effect, or discards it otherwise.
type pooledBody struct {
	io.ReadCloser
	ps        *pooledStream
	t         *HTTPTransport
	keepAlive bool

	mu     sync.Mutex
	closed bool
	failed bool
}

// Read forwards to the underlying body. Tracks read errors so a
// later Close knows not to return a half-drained stream to the pool.
func (b *pooledBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.mu.Lock()
		b.failed = true
		b.mu.Unlock()
	}
	return n, err
}

// Close finalizes the body. If keep-alive is in effect and the body
// was fully drained without I/O error, the underlying stream returns
// to the idle pool. Otherwise it is closed.
func (b *pooledBody) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	failed := b.failed
	b.mu.Unlock()

	// Drain anything left so the next request on the stream sees a
	// clean slate. If the caller didn't fully read the body, draining
	// could block — bound it.
	if !failed {
		_, drainErr := io.Copy(io.Discard, b.ReadCloser)
		if drainErr != nil {
			failed = true
		}
	}

	closeErr := b.ReadCloser.Close()

	switch {
	case failed, !b.keepAlive, closeErr != nil:
		b.ps.discard()
		b.t.untrack(b.ps)
	default:
		b.t.putIdle(b.ps)
	}
	return closeErr
}
