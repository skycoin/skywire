// Package skyroute pkg/skyroute/pool.go c2-net-routing
// connections — browser SOCKS5 requests through the resolving proxy, skysocks-lite
// fetches, skychat messages — don't each pay a fresh route setup.
//
// A skywire routing rule expires after DefaultRouteKeepAlive (10 min) unless an
// OPEN RouteGroup's keepalive loop keeps re-arming every hop. The resolving proxy
// historically dialed a fresh route per connection and closed it when the
// connection ended, so multihop routes died and every page load re-ran
// route-finding + per-hop rule setup (seconds). Direct routes re-set-up instantly,
// which is why "it only worked for direct routes".
//
// Pool fixes that by holding ONE route group per destination open and multiplexing
// many logical connections over it with yamux — exactly the shape skysocks-lite
// already used privately. The held route group's keepalive keeps the multihop hops
// warm, so subsequent connections reuse it with zero setup. The route lives on the
// visor (not the browser), so this needs no "page is open" signal and fixes the
// external-browser SOCKS5 case and the in-tab iframe case identically. An idle
// route group is reclaimed after IdleTTL; a holder that knows it's done (an iframe
// window closing) can Release it eagerly.
//
// The far end must run the yamux-aware skyfwd server (skyenv.SkyForwardingMuxPort).
// Against older visors that don't, the first dial fails, OpenStream returns
// ErrNoMux (negative-cached briefly), and the caller falls back to a 1:1 dial.
package skyroute

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ErrNoMux means the destination's visor does not serve the yamux forwarding port
// (an older build). The caller should fall back to a 1:1 SkyForwardingServerPort
// dial. The pool negative-caches this per destination so it isn't re-probed on
// every request.
var ErrNoMux = errors.New("skyroute: destination does not serve the mux forwarding port")

const (
	// DefaultIdleTTL is how long a route group is held open after its last stream
	// closes before the reaper reclaims it. The route's keepalive keeps the hops
	// warm during this window so reconnects reuse it with no setup.
	DefaultIdleTTL = 10 * time.Minute
	// negativeCacheTTL is how long a destination found to lack mux support is
	// skipped (returns ErrNoMux without re-dialing) before being re-probed.
	negativeCacheTTL = 5 * time.Minute
	// firstStreamTimeout bounds the first yamux stream open, so a route dialed to a
	// non-listening mux port is detected as ErrNoMux quickly instead of blocking on
	// yamux keepalive (~30s).
	firstStreamTimeout = 12 * time.Second
)

// DialRoute dials the destination's forwarding server on muxPort over a (possibly
// multihop) route and returns the route-group net.Conn. The pool yamux-muxes over
// whatever conn this returns; it can take several seconds (route setup) and is
// always called without the pool lock held.
type DialRoute func(ctx context.Context, dest cipher.PubKey, muxPort uint16) (net.Conn, error)

type heldSession struct {
	sess       *yamux.Session
	lastActive time.Time // last time this route group had ≥1 stream (or was opened)
}

// Pool holds and reuses route groups keyed by destination PK.
type Pool struct {
	dial    DialRoute
	idleTTL time.Duration
	log     logrus.FieldLogger

	mu       sync.Mutex
	sessions map[cipher.PubKey]*heldSession
	negative map[cipher.PubKey]time.Time // dest -> earliest re-probe time
	closed   bool
	stop     chan struct{}
}

// New builds a Pool. idleTTL ≤ 0 uses DefaultIdleTTL. A nil log discards output.
func New(dial DialRoute, idleTTL time.Duration, log logrus.FieldLogger) *Pool {
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	if log == nil {
		l := logrus.New()
		l.SetOutput(discard{})
		log = l
	}
	p := &Pool{
		dial:     dial,
		idleTTL:  idleTTL,
		log:      log,
		sessions: map[cipher.PubKey]*heldSession{},
		negative: map[cipher.PubKey]time.Time{},
		stop:     make(chan struct{}),
	}
	go p.reapLoop()
	return p
}

// OpenStream returns a stream to dest's skyfwd server over a HELD, reused route
// group, dialing + caching the route group on first use (or after the previous one
// died). The caller then runs the skynet handshake (skynetweb.PerformHandshake) on
// the returned conn exactly as it would on a fresh route. Returns ErrNoMux when
// dest doesn't serve the mux port (caller should fall back to a 1:1 dial).
func (p *Pool) OpenStream(ctx context.Context, dest cipher.PubKey) (net.Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("skyroute: pool closed")
	}
	// Reuse an open session.
	if s, ok := p.sessions[dest]; ok {
		if !s.sess.IsClosed() {
			s.lastActive = time.Now()
			sess := s.sess
			p.mu.Unlock()
			if stream, err := sess.Open(); err == nil {
				return stream, nil
			}
			// Session is up but won't yield a stream — drop it and redial below.
			p.mu.Lock()
			if cur, ok := p.sessions[dest]; ok && cur.sess == sess {
				_ = sess.Close() //nolint:errcheck
				delete(p.sessions, dest)
			}
		} else {
			_ = s.sess.Close() //nolint:errcheck
			delete(p.sessions, dest)
		}
	}
	// Skip destinations recently found to lack mux support.
	if t, ok := p.negative[dest]; ok {
		if time.Now().Before(t) {
			p.mu.Unlock()
			return nil, ErrNoMux
		}
		delete(p.negative, dest)
	}
	p.mu.Unlock()

	// Dial the mux forwarding port over a route (may take seconds; no lock held).
	conn, err := p.dial(ctx, dest, skyenv.SkyForwardingMuxPort)
	if err != nil {
		return nil, err
	}
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	// First stream doubles as a liveness probe: if the remote isn't serving the mux
	// port, it won't accept, so bound the open and treat a timeout as ErrNoMux.
	stream, err := openWithTimeout(ctx, sess, firstStreamTimeout)
	if err != nil {
		_ = sess.Close() //nolint:errcheck
		_ = conn.Close() //nolint:errcheck
		p.mu.Lock()
		p.negative[dest] = time.Now().Add(negativeCacheTTL)
		p.mu.Unlock()
		return nil, ErrNoMux
	}
	// Publish, unless a concurrent dial for the same dest already won.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = stream.Close() //nolint:errcheck
		_ = sess.Close()   //nolint:errcheck
		return nil, errors.New("skyroute: pool closed")
	}
	if cur, ok := p.sessions[dest]; ok && !cur.sess.IsClosed() {
		other := cur.sess
		p.mu.Unlock()
		_ = stream.Close() //nolint:errcheck
		_ = sess.Close()   //nolint:errcheck
		if s2, err := other.Open(); err == nil {
			return s2, nil
		}
		return nil, errors.New("skyroute: raced session unusable")
	}
	p.sessions[dest] = &heldSession{sess: sess, lastActive: time.Now()}
	p.mu.Unlock()
	return stream, nil
}

// Release eagerly closes and drops dest's held route group (e.g. an iframe window
// that owned it has closed). Idempotent.
func (p *Pool) Release(dest cipher.PubKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[dest]; ok {
		_ = s.sess.Close() //nolint:errcheck
		delete(p.sessions, dest)
	}
}

// Close tears down the pool and every held route group.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	close(p.stop)
	for dest, s := range p.sessions {
		_ = s.sess.Close() //nolint:errcheck
		delete(p.sessions, dest)
	}
	return nil
}

func (p *Pool) reapLoop() {
	t := time.NewTicker(p.idleTTL / 3)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.reap()
		}
	}
}

// reap closes route groups that have had no streams for longer than idleTTL, and
// expires stale negative-cache entries. A session with live streams has its
// lastActive refreshed so it only idles out after its last stream closes.
func (p *Pool) reap() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for dest, s := range p.sessions {
		if s.sess.IsClosed() {
			delete(p.sessions, dest)
			continue
		}
		if s.sess.NumStreams() > 0 {
			s.lastActive = now
			continue
		}
		if now.Sub(s.lastActive) > p.idleTTL {
			_ = s.sess.Close() //nolint:errcheck
			delete(p.sessions, dest)
			p.log.WithField("dest", dest.String()).Debug("skyroute: released idle route group")
		}
	}
	for dest, t := range p.negative {
		if now.After(t) {
			delete(p.negative, dest)
		}
	}
}

// openWithTimeout runs sess.Open bounded by the smaller of d and ctx, closing the
// session on timeout so the blocked Open unwinds.
func openWithTimeout(ctx context.Context, sess *yamux.Session, d time.Duration) (net.Conn, error) {
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		// A yamux round-trip is the definitive liveness probe: the far-end's yamux
		// layer auto-answers the ping, so a destination not running a mux server (or
		// a dead route) fails here. Open() alone is unreliable — it returns a stream
		// optimistically before a dead session is noticed, which surfaced as a flaky
		// ErrNoMux-vs-nil race under -race.
		if _, perr := sess.Ping(); perr != nil {
			ch <- res{nil, perr}
			return
		}
		c, err := sess.Open()
		ch <- res{c, err}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-timer.C:
		return nil, errors.New("skyroute: first stream open timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
