// Package skysocks pkg/skysocks/client.go c4-app-proxy
package skysocks

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // RFC6455 mandates SHA-1 for the WebSocket accept key
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
)

// muxStreamWindowBytes is the per-stream yamux flow-control window skysocks
// advertises, raised from yamux's 256 KB default. Throughput over a reliable
// stream is bounded by window / RTT (the bandwidth-delay product); the skywire
// mesh path has a high RTT (~0.5-1 s end-to-end across relays), so the 256 KB
// default caps a single skysocks stream at ~256 KB/s regardless of the
// underlying link — measured live at ~250 KB/s on a 100 Mbps card whose exit
// egressed at 200 MB/s. 16 MB gives 16 MB/s at 1 s RTT (128 Mbps), saturating a
// 100 Mbps card with headroom; yamux only buffers up to the window's worth of
// ACTUALLY in-flight data, so idle/interactive streams pay nothing and a bulk
// stream costs at most this much receive buffer. The receiver's window governs
// each direction, so the client value speeds downloads and the server value
// speeds uploads.
const muxStreamWindowBytes = 16 * 1024 * 1024

// Client implement multiplexing proxy client using yamux.
//
// The client holds N independent yamux sessions ("tunnels"), each over its own
// dialed route-group conn. Accepted browser connections are striped across the
// tunnels by pickSession (least-loaded live tunnel), so throughput sums across
// disjoint routes with zero cross-tunnel reorder — the connection-striped
// aggregation design in docs/mux_aggregation_rfc.md. N is configurable; the
// default is a single tunnel (N==1), which is byte-for-byte the pre-aggregation
// behavior. Multiple tunnels only genuinely aggregate once they leave over
// disjoint first-hop transports; that disjoint-dial coordination is the required
// follow-up (RFC step 3) and is NOT implemented here.
type Client struct {
	appCl *app.Client
	// sessions holds the live tunnels (>=1). Guarded by sessionsMu because
	// AddTunnel appends while the accept loop / keepalive read concurrently.
	sessions   []*yamux.Session
	sessionsMu sync.Mutex
	listener   net.Listener
	once       sync.Once
	closeC     chan struct{}

	// streams tracks the currently open tunneled streams so the status page can
	// expand the "N open stream(s)" count into per-stream rows (id + CONNECT
	// target + age). yamux does not meter per-stream bytes, so bytes are not
	// tracked here; only the cheap identity/target/age the sniff already parses.
	streamsMu sync.Mutex
	streams   map[uint32]streamMeta
}

// streamMeta is the per-stream detail the status page surfaces for an open
// tunneled stream.
type streamMeta struct {
	target string
	since  time.Time
}

// errAllTunnelsDown is the synthetic stream-open error used when every tunnel
// is closed so pickSession returns nil and there is no live session to Open() a
// real error from. It drives the same route-down interstitial + reconnect path a
// single closed session took before.
var errAllTunnelsDown = errors.New("all tunnels to the exit are down")

// newYamuxSession wraps a dialed route-group conn in a yamux client session with
// skysocks's flow-control window. Shared by NewClient and AddTunnel so every
// tunnel is configured identically.
func newYamuxSession(conn net.Conn) (*yamux.Session, error) {
	sessionCfg := yamux.DefaultConfig()
	sessionCfg.EnableKeepAlive = false
	sessionCfg.MaxStreamWindowSize = muxStreamWindowBytes
	session, err := yamux.Client(conn, sessionCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating client: yamux: %w", err)
	}
	return session, nil
}

// NewClient constructs a new single-tunnel Client. Signature unchanged: this is
// the common case and every existing caller/test builds one tunnel this way. Use
// AddTunnel (or NewMultiClient) to stripe browser connections across N tunnels.
func NewClient(conn net.Conn, appCl *app.Client) (*Client, error) {
	c := &Client{
		appCl:   appCl,
		closeC:  make(chan struct{}),
		streams: make(map[uint32]streamMeta),
	}

	session, err := newYamuxSession(conn)
	if err != nil {
		return nil, err
	}
	c.sessions = []*yamux.Session{session}

	go c.sessionKeepAliveLoop()

	return c, nil
}

// NewMultiClient constructs a Client striping across one tunnel per conn. With a
// single conn it is identical to NewClient. The N conns must already be dialed
// (ideally over disjoint routes — see the disjoint-dial follow-up in
// docs/mux_aggregation_rfc.md step 3); this constructor only wraps them.
func NewMultiClient(conns []net.Conn, appCl *app.Client) (*Client, error) {
	if len(conns) == 0 {
		return nil, errors.New("skysocks: NewMultiClient needs at least one conn")
	}
	c, err := NewClient(conns[0], appCl)
	if err != nil {
		return nil, err
	}
	for _, conn := range conns[1:] {
		if err := c.AddTunnel(conn); err != nil {
			_ = c.Close() //nolint:errcheck
			return nil, err
		}
	}
	return c, nil
}

// AddTunnel wraps an additional dialed route-group conn in a yamux session and
// appends it to the tunnel set. The shared keepalive loop and pickSession pick it
// up automatically. This is the extension point the disjoint-dial coordinator
// (RFC step 3) will call to grow the tunnel set at runtime.
func (c *Client) AddTunnel(conn net.Conn) error {
	session, err := newYamuxSession(conn)
	if err != nil {
		return err
	}
	c.sessionsMu.Lock()
	c.sessions = append(c.sessions, session)
	c.sessionsMu.Unlock()
	return nil
}

// snapshotSessions returns a copy of the current tunnel set for lock-free
// iteration by callers (keepalive, status).
func (c *Client) snapshotSessions() []*yamux.Session {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	out := make([]*yamux.Session, len(c.sessions))
	copy(out, c.sessions)
	return out
}

// leastLoaded returns the index of the smallest non-negative count, or -1 when
// every count is negative — the sentinel a closed/skipped tunnel is given. Ties
// resolve to the lowest index so selection is deterministic. Extracted as a pure
// function so the least-loaded striping policy is unit-testable without a live
// yamux session.
func leastLoaded(counts []int) int {
	best := -1
	for i, n := range counts {
		if n < 0 {
			continue
		}
		if best == -1 || n < counts[best] {
			best = i
		}
	}
	return best
}

// pickSession returns the least-loaded live tunnel (fewest open yamux streams),
// skipping any closed tunnel, or nil when every tunnel is closed. This is the
// connection-striping policy: each accepted browser conn goes to the tunnel with
// the most spare capacity. With a single tunnel it always returns that tunnel
// while it is live (identical to the pre-aggregation c.session).
func (c *Client) pickSession() *yamux.Session {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	if len(c.sessions) == 0 {
		return nil
	}
	counts := make([]int, len(c.sessions))
	for i, s := range c.sessions {
		if s == nil || s.IsClosed() {
			counts[i] = -1
			continue
		}
		counts[i] = s.NumStreams()
	}
	idx := leastLoaded(counts)
	if idx < 0 {
		return nil
	}
	return c.sessions[idx]
}

// anySessionLive reports whether at least one tunnel is still up. With a single
// tunnel this is exactly !session.IsClosed().
func (c *Client) anySessionLive() bool {
	for _, s := range c.snapshotSessions() {
		if s != nil && !s.IsClosed() {
			return true
		}
	}
	return false
}

// allSessionsClosed reports whether every tunnel is closed (the whole-client
// teardown / reconnect trigger). An empty set counts as closed.
func (c *Client) allSessionsClosed() bool {
	return !c.anySessionLive()
}

// totalStreams sums the open stream counts across live tunnels — the aggregate
// "N open stream(s)" the status page reports.
func (c *Client) totalStreams() int {
	total := 0
	for _, s := range c.snapshotSessions() {
		if s != nil && !s.IsClosed() {
			total += s.NumStreams()
		}
	}
	return total
}

// ListenAndServe start tcp listener on addr and proxies incoming
// connection to a remote proxy server.
func (c *Client) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		if c.appCl != nil {
			c.setAppError(err)
		}
		return fmt.Errorf("listen: %w", err)
	}

	if c.appCl != nil {
		c.appCl.Log().Infof("Listening skysocks client on %s", addr)
	}

	c.listener = l
	go func() {
		<-c.closeC
		l.Close() //nolint:errcheck,gosec
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if c.appCl != nil {
				c.appCl.Log().Errorf("Error accepting: %v", err)
			}
			// Release the yamux session + its keepalive goroutine + the
			// underlying conn on Accept failure, mirroring the session.Open
			// error path below. Without this, a listener that fails
			// independently of an orderly Close (so closeC was never
			// signaled) leaked the whole session. c.close() is sync.Once-
			// guarded, so it's a no-op when shutdown already triggered this.
			c.close()
			return fmt.Errorf("accept: %w", err)
		}

		if c.appCl != nil {
			c.appCl.Log().Debug("Accepted skysocks client")
		}

		// Stripe onto the least-loaded live tunnel. pickSession returns nil only
		// when every tunnel is closed; in that (route-down) case there is no live
		// session to Open() a real error from, so a sentinel drives the same
		// interstitial + reconnect path a single closed session took before.
		sess := c.pickSession()
		var stream net.Conn
		if sess != nil {
			stream, err = sess.Open()
		} else {
			err = errAllTunnelsDown
		}
		if sess == nil || err != nil {
			// The mesh route/session to the exit is down (exit restart, all
			// mux legs dropped). Before tearing down for reconnect, serve the
			// waiting browser a branded "building a route over skywire…"
			// interstitial for a plaintext-HTTP request so it retries once the
			// route is back, instead of a bare connection failure. Best-effort
			// and deadline-bounded (see ServeSOCKS5); declined for HTTPS/other
			// ports. Runs in a goroutine that owns conn so the reconnect below
			// isn't delayed by a slow browser. The status.skysocks override
			// keeps the in-process status page reachable even now (exit down)
			// instead of being shadowed by the interstitial — status.skysocks
			// needs no exit stream, and this is exactly when the user wants it.
			// A single Open failing does NOT always mean all tunnels are dead: if
			// ANY tunnel is still up, the failure was transient (or just this
			// tunnel died) and the exit is still reachable. In that case
			// ServeSOCKS5 serves a fall-through reload (exitReachable=true)
			// instead of pinning the browser on the waiting interstitial, and we
			// keep listening rather than tearing down — the browser's reload gets
			// a working stream on the next-picked tunnel. Only when EVERY tunnel
			// is closed do we serve the waiting interstitial and trigger
			// reconnect. With a single tunnel (N==1) this is exactly the prior
			// behavior.
			reachable := c.anySessionLive()
			go func(bc net.Conn) {
				if serr := proxyinterstitial.ServeSOCKS5(bc, proxyinterstitial.StatusLine(err), "skysocks", c.statusOverride, c.exitReachable); serr != nil && c.appCl != nil {
					c.appCl.Log().Debugf("route-down interstitial not served: %v", serr)
				}
				bc.Close() //nolint:errcheck,gosec
			}(conn)
			if reachable {
				if c.appCl != nil {
					c.appCl.Log().Debugf("yamux stream open failed but a tunnel is up; keeping listener: %v", err)
				}
				continue
			}
			c.close()

			return fmt.Errorf("error opening yamux stream: %w", err)
		}

		if c.appCl != nil {
			c.appCl.Log().Debug("Opened session skysocks client")
		}

		go c.handleStream(conn, stream)
	}
}

// Liveness-probe tuning for sessionKeepAliveLoop. A route group can be
// torn down router-side (remote visor restart, all mux legs dropped)
// WITHOUT the underlying conn delivering EOF, so session.IsClosed() may
// never flip and ListenAndServe would block forever in Accept(). A timed
// yamux ping detects that. The interval/timeout are generous and we
// require consecutive failures so a merely-slow multihop route is not
// mistaken for a dead one (a false close just costs one reconnect cycle).
const (
	livenessProbeInterval = 15 * time.Second
	livenessProbeTimeout  = 10 * time.Second
	livenessFailThreshold = 2
)

// sessionKeepAliveLoop probes every tunnel and retires the dead ones. A tunnel
// that fails livenessFailThreshold consecutive pings is closed so pickSession
// stops routing to it (dropping just that tunnel's streams); the whole client is
// torn down for reconnect only once EVERY tunnel is closed. With a single tunnel
// (N==1) this is exactly the prior behavior: the one tunnel dying closes the
// client. Per-tunnel re-dial of a dropped tunnel (instead of only skipping it) is
// the disjoint-dial follow-up — see docs/mux_aggregation_rfc.md step 3.
func (c *Client) sessionKeepAliveLoop() {
	ticker := time.NewTicker(livenessProbeInterval)
	defer ticker.Stop()

	fails := make(map[*yamux.Session]int)
	for {
		select {
		case <-c.closeC:
			return
		case <-ticker.C:
			for _, s := range c.snapshotSessions() {
				if s.IsClosed() {
					delete(fails, s)
					continue
				}
				if sessionPingAlive(s, livenessProbeTimeout) {
					fails[s] = 0
					continue
				}
				fails[s]++
				if fails[s] >= livenessFailThreshold {
					if c.appCl != nil {
						c.appCl.Log().Warnf("Liveness probe failed %dx; tunnel gone, retiring it", fails[s])
					}
					// Retire just this tunnel; a closed session is skipped by
					// pickSession. The whole-client close below fires only when
					// this leaves no live tunnel.
					_ = s.Close() //nolint:errcheck
					delete(fails, s)
				}
			}
			if c.allSessionsClosed() {
				c.close()

				return
			}
		}
	}
}

// sessionPingAlive reports whether a yamux ping round-trips on the given session
// within timeout. A silently torn-down rg conn never pongs; the timer catches
// that. The ping runs in a goroutine so a wedged ping cannot stall the caller —
// Close() tears down the session, which unblocks the pending ping.
func sessionPingAlive(s *yamux.Session, timeout time.Duration) bool {
	done := make(chan error, 1)
	go func() {
		_, err := s.Ping()
		done <- err
	}()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(timeout):
		return false
	}
}

// sessionAlive reports whether any tunnel answers a ping within timeout. With a
// single tunnel it is exactly a ping of that tunnel (retained for the liveness
// tests and any single-session caller).
func (c *Client) sessionAlive(timeout time.Duration) bool {
	for _, s := range c.snapshotSessions() {
		if s != nil && sessionPingAlive(s, timeout) {
			return true
		}
	}
	return false
}

func (c *Client) handleStream(conn, stream net.Conn) {
	// Transparently sniff the SOCKS5 CONNECT target so a request for the reserved
	// status.skysocks host is answered in-process instead of tunneled to the
	// exit. For every non-status request the greeting/method negotiation and the
	// CONNECT request are forwarded to the exit byte-for-byte, so the exit sees an
	// identical stream — only status.skysocks diverges.
	proceed, target := c.sniffSOCKS5Status(conn, stream)
	if !proceed {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		if c.allSessionsClosed() {
			c.close()
		}
		return
	}

	// Track this stream for the status page's per-stream detail (id + target +
	// age). Registered here, deregistered when the splice below returns.
	if id, ok := streamID(stream); ok {
		c.addStream(id, target)
		defer c.removeStream(id)
	}

	const errorCount = 2

	errCh := make(chan error, errorCount)

	go func() {
		_, err := io.Copy(stream, conn)
		errCh <- err
	}()

	go func() {
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()

	// Wait for both io.Copy goroutines to finish (exactly 2 sends).
	for i := 0; i < errorCount; i++ {
		if err := <-errCh; err != nil && c.appCl != nil {
			c.appCl.Log().Debugf("Copy error: %v", err)
		}
		// Close both sides after the first copy finishes to unblock the other.
		if i == 0 {
			conn.Close()   //nolint:errcheck,gosec
			stream.Close() //nolint:errcheck,gosec
		}
	}

	if c.allSessionsClosed() {
		c.close()
	}
}

// statusSniffTimeout bounds the SOCKS5 handshake sniff so a wedged or
// non-SOCKS5 client can't pin a handleStream goroutine. The greeting and CONNECT
// request are sent right after connect, so this window is generous; it is
// cleared before the bidirectional data splice, which stays deadline-free.
const statusSniffTimeout = 15 * time.Second

// sniffSOCKS5Status inspects the SOCKS5 CONNECT target to intercept the reserved
// status.skysocks host, and CRUCIALLY does so WITHOUT any round-trip to the exit:
// the method-selection reply is answered LOCALLY (no-auth) so the CONNECT target
// can be read and a status.skysocks request served in-process even when the exit
// is dead or unreachable — which is exactly when the status page matters most. The
// exit is contacted only AFTER a non-status target is confirmed; at that point the
// greeting and CONNECT request are replayed to the exit byte-for-byte, so the exit
// sees an identical stream and non-status traffic is a plain tunnel.
//
// A browser that offers no no-auth method (exotic for a loopback SOCKS client) can
// not be answered locally; that case falls back to the transparent forward-to-exit
// handshake, where the exit drives the (auth) negotiation and everything rides
// through. Such a client is never the browser hitting status.skysocks.
//
// Returns proceed=true when the caller should continue with the normal
// bidirectional splice (conn and stream are positioned just past the handshake);
// target is then the CONNECT "host:port" the stream carries (or "" when it could
// not be parsed), for the status page's per-stream detail. Returns false when the
// request was served in-process or the connection is unusable; the caller then
// closes both sides.
func (c *Client) sniffSOCKS5Status(conn, stream net.Conn) (proceed bool, target string) {
	// Only the browser side gets a read deadline up front: the exit must not be
	// touched (nor block us) until a non-status target is confirmed, so the reserved
	// status host stays reachable regardless of exit reachability.
	_ = conn.SetReadDeadline(time.Now().Add(statusSniffTimeout)) //nolint:errcheck

	// Greeting: VER, NMETHODS, METHODS[NMETHODS]. Buffered so a non-status target's
	// greeting can be replayed to the exit byte-for-byte.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 0x05 {
		return false, ""
	}
	greeting := make([]byte, 2+int(hdr[1]))
	greeting[0], greeting[1] = hdr[0], hdr[1]
	if _, err := io.ReadFull(conn, greeting[2:]); err != nil {
		return false, ""
	}

	// If the browser did not offer no-auth we can't answer locally; fall back to
	// forwarding the handshake to the exit and letting it drive the negotiation.
	// (A status.skysocks browser always offers no-auth, so this never shadows it.)
	if !offersNoAuth(greeting) {
		return c.forwardExitHandshake(conn, stream, greeting)
	}
	// Answer method-selection to the browser ourselves (no-auth) so the CONNECT
	// target can be read WITHOUT contacting the exit.
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return false, ""
	}

	// CONNECT request: VER, CMD, RSV, ATYP, ADDR, PORT. Buffered so a non-status
	// target is replayed to the exit byte-for-byte.
	rhdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, rhdr); err != nil || rhdr[0] != 0x05 {
		return false, ""
	}
	req := append([]byte{}, rhdr...)
	var host string
	switch rhdr[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return false, ""
		}
		host = net.IP(b).String()
		req = append(req, b...)
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return false, ""
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return false, ""
		}
		host = string(b)
		req = append(req, l[0])
		req = append(req, b...)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return false, ""
		}
		host = net.IP(b).String()
		req = append(req, b...)
	default:
		// Unknown ATYP: not a status host — open the exit handshake, forward what
		// we have, and let the exit deal with the rest via the splice.
		if err := c.openExit(stream, greeting); err != nil {
			return false, ""
		}
		if _, err := stream.Write(req); err != nil {
			return false, ""
		}
		clearDeadlines(conn, stream)
		return true, ""
	}
	portB := make([]byte, 2)
	if _, err := io.ReadFull(conn, portB); err != nil {
		return false, ""
	}
	req = append(req, portB...)
	port := int(portB[0])<<8 | int(portB[1])

	// Reserved status host: serve the in-process page over HTTP. This is reached
	// with NO exit involvement, so status.skysocks stays reachable when the exit is
	// down. HTTP only — the resolver CA forbids a .skysocks TLS leaf, so
	// status.skysocks is HTTP-only by design. serveStatusPage routes "/" (page) and
	// "/ws" (live WebSocket) on the browser's request.
	if surface, ok := proxystatus.Match(host); ok && surface == proxystatus.SurfaceSkysocks {
		clearDeadlines(conn, stream)
		c.serveStatusPage(conn, stream)
		return false, ""
	}

	// Non-status: open the exit's SOCKS session now (greeting + method reply) and
	// replay the buffered CONNECT request, then splice.
	if err := c.openExit(stream, greeting); err != nil {
		return false, ""
	}
	if _, err := stream.Write(req); err != nil {
		return false, ""
	}
	clearDeadlines(conn, stream)
	return true, fmt.Sprintf("%s:%d", host, port)
}

// offersNoAuth reports whether a buffered SOCKS5 greeting (VER, NMETHODS, METHODS…)
// advertises the no-authentication method (0x00) — the one this transparent client
// can answer locally without consulting the exit.
func offersNoAuth(greeting []byte) bool {
	if len(greeting) < 2 {
		return false
	}
	return bytes.IndexByte(greeting[2:], 0x00) >= 0
}

// openExit performs the client→exit SOCKS5 method negotiation for a confirmed
// non-status target: it replays the browser's greeting to the exit and consumes
// the exit's method-selection reply, which the skysocks exit (no-auth) answers
// with 05 00. It is called only after the local browser handshake, so it never
// gates recognition of the reserved status host on the exit being reachable. A
// read deadline bounds a dead exit so it can't wedge the goroutine.
func (c *Client) openExit(stream net.Conn, greeting []byte) error {
	_ = stream.SetReadDeadline(time.Now().Add(statusSniffTimeout)) //nolint:errcheck
	if _, err := stream.Write(greeting); err != nil {
		return err
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(stream, method); err != nil {
		return err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return fmt.Errorf("exit selected non-no-auth method %v", method)
	}
	return nil
}

// forwardExitHandshake is the transparent fallback for a browser that offers no
// no-auth method: the greeting is forwarded to the exit verbatim and the exit's
// method-selection reply is forwarded back to the browser, then the connection is
// spliced so the (auth) sub-negotiation and CONNECT ride through end-to-end. Such a
// client is never the loopback browser hitting status.skysocks, so leaving status
// interception off this path is correct.
func (c *Client) forwardExitHandshake(conn, stream net.Conn, greeting []byte) (proceed bool, target string) {
	_ = stream.SetReadDeadline(time.Now().Add(statusSniffTimeout)) //nolint:errcheck
	if _, err := stream.Write(greeting); err != nil {
		return false, ""
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(stream, method); err != nil {
		return false, ""
	}
	if _, err := conn.Write(method); err != nil {
		return false, ""
	}
	clearDeadlines(conn, stream)
	return true, ""
}

// streamID extracts the yamux stream id from the net.Conn session.Open returns
// (a *yamux.Stream), for the status page's per-stream detail. Best-effort: an
// unexpected conn type yields ok=false and the stream simply isn't tracked.
func streamID(stream net.Conn) (uint32, bool) {
	if s, ok := stream.(interface{ StreamID() uint32 }); ok {
		return s.StreamID(), true
	}
	return 0, false
}

// addStream/removeStream/streamSnapshot maintain the open-stream registry the
// status page reads. All are mutex-guarded and cheap (no per-stream byte
// metering — yamux does not expose it).
func (c *Client) addStream(id uint32, target string) {
	c.streamsMu.Lock()
	if c.streams == nil {
		c.streams = make(map[uint32]streamMeta)
	}
	c.streams[id] = streamMeta{target: target, since: time.Now()}
	c.streamsMu.Unlock()
}

func (c *Client) removeStream(id uint32) {
	c.streamsMu.Lock()
	delete(c.streams, id)
	c.streamsMu.Unlock()
}

// streamSnapshot returns the currently open streams as sorted proxystatus.Stream
// rows (by id) for the status page.
func (c *Client) streamSnapshot() []proxystatus.Stream {
	c.streamsMu.Lock()
	defer c.streamsMu.Unlock()
	if len(c.streams) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]proxystatus.Stream, 0, len(c.streams))
	for id, m := range c.streams {
		out = append(out, proxystatus.Stream{
			ID:     id,
			Target: m.target,
			AgeMS:  now.Sub(m.since).Milliseconds(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// exitReachable reports whether at least one tunnel to the exit is currently
// live. It is the fall-through probe handed to proxyinterstitial.ServeSOCKS5:
// when the exit is reachable again, the interstitial is replaced by a reload page
// so the browser proceeds to the intended destination instead of waiting on a
// spinner. With a single tunnel it is exactly !session.IsClosed().
func (c *Client) exitReachable() bool {
	return c.anySessionLive()
}

// statusOverride is the reserved-host answer handed to
// proxyinterstitial.ServeSOCKS5's route-down path: for the status.skysocks host
// it returns the rendered status page (the same one-shot bytes serveStatusPage
// writes), so the page stays reachable while the exit stream is down instead of
// being shadowed by the interstitial. Any other host returns nil, leaving the
// interstitial in place. statusSnapshot already reports "no active session to
// the exit" when the session is down, which is the correct content here. (Only
// the one-shot page is served on this path — the live WebSocket needs a duplex
// stream that the override's fixed-body model can't carry; the page's inline
// script reconnects the WebSocket once the conn is back.)
func (c *Client) statusOverride(host string) []byte {
	if surface, ok := proxystatus.Match(host); ok && surface == proxystatus.SurfaceSkysocks {
		return statusHTTPResponse(proxystatus.Render(c.statusSnapshot()))
	}
	return nil
}

// serveStatusPage completes the SOCKS5 CONNECT with a success reply, reads the
// browser's HTTP request, and routes on its path: "/ws" upgrades to a live,
// bidirectional WebSocket (serveStatusWS); anything else gets the one-shot
// rendered status.skysocks page. Best-effort: any write failure just drops the
// conn.
func (c *Client) serveStatusPage(conn, stream net.Conn) {
	// CONNECT success with a dummy BND.ADDR/PORT so the browser proceeds to send
	// its HTTP request.
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	// Read the request so we can route on its path. status.skysocks is HTTP-only
	// (the resolver CA forbids a .skysocks TLS leaf), so this is plaintext; a GET's
	// request line + headers arrive in the first packet, so one bounded read is
	// enough.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	buf := make([]byte, 2048)
	n, _ := conn.Read(buf)                //nolint:errcheck // best-effort; page is fixed
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	if statusRequestPath(buf[:n]) == "/ws" {
		// status is served entirely in-process, so the exit-side yamux stream is
		// unused — close it now so the long-lived WS loop doesn't pin one open.
		stream.Close() //nolint:errcheck,gosec
		if !wsHandshake(conn, buf[:n]) {
			return
		}
		c.serveStatusWS(conn)
		return
	}

	body := proxystatus.Render(c.statusSnapshot())
	_, _ = conn.Write(statusHTTPResponse(body)) //nolint:errcheck
}

// statusRequestPath extracts the request-target path from an HTTP request line
// ("METHOD SP PATH SP VERSION"), stripping any query string. It defaults to "/"
// for anything it can't parse, so a malformed request falls through to the
// full-page render rather than the WebSocket upgrade.
func statusRequestPath(req []byte) string {
	line := req
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := bytes.Fields(line)
	if len(fields) < 2 {
		return "/"
	}
	p := fields[1]
	if i := bytes.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return string(p)
}

// --- status.skysocks WebSocket control channel -------------------------------
//
// The live status region is pushed to the browser over a WebSocket (RFC6455)
// carried on the hijacked plaintext SOCKS stream, replacing the earlier one-way
// SSE stream. WebSocket is bidirectional: the same conn carries fragment pushes
// server→browser AND control commands browser→server, so the page can become a
// proxy control surface. The handshake and framing are hand-rolled (a few dozen
// lines) rather than pulling in a library, because the conn is an already-
// hijacked raw net.Conn with no http.ResponseWriter for a library's server
// Accept() to hijack.

// WebSocket loop tuning. wsPushInterval is how often a fresh live-region fragment
// is pushed — ~1s is responsive enough to watch a route warm without thrashing.
// wsPingInterval keeps an idle conn alive (the browser auto-replies PONG, which
// also resets the read deadline). wsReadTimeout must exceed wsPingInterval so an
// idle-but-healthy conn is not torn down; wsWriteTimeout bounds each write so a
// wedged browser errors out instead of blocking forever.
const (
	wsPushInterval = time.Second
	wsPingInterval = 25 * time.Second
	wsReadTimeout  = 90 * time.Second
	wsWriteTimeout = 10 * time.Second
)

// RFC6455 opcodes (only the ones this endpoint uses).
const (
	wsOpText  = 0x1
	wsOpClose = 0x8
	wsOpPing  = 0x9
	wsOpPong  = 0xA
)

// wsGUID is the RFC6455 magic appended to Sec-WebSocket-Key before the SHA-1.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsHandshake completes the RFC6455 opening handshake over the hijacked plaintext
// stream: it reads the client's Sec-WebSocket-Key out of the already-buffered
// request bytes and replies "101 Switching Protocols" with the computed
// Sec-WebSocket-Accept (base64(sha1(key + wsGUID))). Returns false (dropping the
// conn) when the request is not a well-formed upgrade.
func wsHandshake(conn net.Conn, req []byte) bool {
	key := httpHeaderValue(req, "Sec-WebSocket-Key")
	if key == "" || !strings.EqualFold(httpHeaderValue(req, "Upgrade"), "websocket") {
		return false
	}
	sum := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec // RFC6455 mandates SHA-1 here
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
	_, err := conn.Write([]byte(resp))
	_ = conn.SetWriteDeadline(time.Time{}) //nolint:errcheck
	return err == nil
}

// httpHeaderValue returns the value of the named header (case-insensitive) from a
// raw HTTP request, or "" if absent.
func httpHeaderValue(req []byte, name string) string {
	for _, ln := range strings.Split(string(req), "\n") {
		ln = strings.TrimRight(ln, "\r")
		i := strings.IndexByte(ln, ':')
		if i < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ln[:i]), name) {
			return strings.TrimSpace(ln[i+1:])
		}
	}
	return ""
}

// wsConn serializes server→client frame writes (the push loop, PONG replies and
// resync responses all share one conn) behind a mutex.
type wsConn struct {
	conn net.Conn
	mu   sync.Mutex
}

// write emits one server→client frame (never masked, per RFC6455) with a bounded
// write deadline.
func (w *wsConn) write(opcode byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
	return wsWriteFrame(w.conn, opcode, payload)
}

// statusControlCmd is the JSON shape of a browser→server control frame on the
// status.skysocks WebSocket, e.g. {"cmd":"resync"}. Kept deliberately small; the
// mux-control commands add fields here when they land (see handleStatusControl).
type statusControlCmd struct {
	Cmd string `json:"cmd"`
}

// serveStatusWS runs the bidirectional status WebSocket over the hijacked
// (already-upgraded) plaintext SOCKS stream. A reader goroutine consumes
// browser→server frames (control commands, PING, CLOSE) while the main loop
// pushes a fresh live-region fragment every wsPushInterval and a keepalive PING
// every wsPingInterval. It returns — releasing this goroutine, the reader and the
// conn — when the browser goes away (read/write error or CLOSE) or the client
// shuts down (closeC), so a session reconnect or proxy restart cannot leak it.
// The loop is ctx/timer-light (two tickers, no per-tick goroutine) so it is safe
// under the single-threaded wasm runtime, though that path is not normally
// exercised there.
func (c *Client) serveStatusWS(conn net.Conn) {
	w := &wsConn{conn: conn}
	stopC := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopC) }) }

	// Reader: browser→server frames. Blocks in wsReadFrame; the read deadline
	// (reset each iteration) plus closeC/stopC guarantee it can't wedge.
	go func() {
		defer stop()
		for {
			select {
			case <-stopC:
				return
			case <-c.closeC:
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout)) //nolint:errcheck
			op, payload, err := wsReadFrame(conn)
			if err != nil {
				return
			}
			switch op {
			case wsOpText:
				c.handleStatusControl(payload, w)
			case wsOpPing:
				if err := w.write(wsOpPong, payload); err != nil {
					return
				}
			case wsOpClose:
				return
			default:
				// wsOpPong / continuation / binary: ignore.
			}
		}
	}()

	// Push once immediately so the browser syncs without waiting a full tick.
	if err := w.write(wsOpText, proxystatus.RenderFragment(c.statusSnapshot())); err != nil {
		stop()
		return
	}
	push := time.NewTicker(wsPushInterval)
	ping := time.NewTicker(wsPingInterval)
	defer push.Stop()
	defer ping.Stop()
	for {
		select {
		case <-c.closeC:
			stop()
			return
		case <-stopC:
			return
		case <-push.C:
			if err := w.write(wsOpText, proxystatus.RenderFragment(c.statusSnapshot())); err != nil {
				stop()
				return
			}
		case <-ping.C:
			if err := w.write(wsOpPing, nil); err != nil {
				stop()
				return
			}
		}
	}
}

// handleStatusControl dispatches a browser→server control frame (JSON, e.g.
// {"cmd":"resync"}). This is the seam that turns the read-only status page into a
// proxy control surface. Only SAFE, already-available actions are wired here:
//
//   - "resync": push a fresh live-region fragment immediately.
//
// TODO(mux-control): the mux-op commands the page previews as disabled buttons —
// "add_leg", "drop_leg", "mux_mode", "rebuild" — are the next step. They mutate
// the surface's route group and so need an app→visor mux-control RPC that does not
// exist yet. The building blocks are already present (visor.RouteGroupMuxInfo for
// the current legs, router.AddMuxRoute to grow one, and the `cli proxy mux set`
// reconcile path for mode/rebuild); exposing them over the app RPC as a mutating
// proxystatus.Provider method is OUT OF SCOPE here. When that lands, add the cases
// below and enable the matching buttons in pkg/proxystatus/render.go.
func (c *Client) handleStatusControl(payload []byte, w *wsConn) {
	var cmd statusControlCmd
	if err := json.Unmarshal(payload, &cmd); err != nil {
		return
	}
	switch cmd.Cmd {
	case "resync":
		_ = w.write(wsOpText, proxystatus.RenderFragment(c.statusSnapshot())) //nolint:errcheck
	case "add_leg", "drop_leg", "mux_mode", "rebuild":
		// TODO(mux-control): wire once the app→visor mux-control RPC exists.
	default:
		// Unknown/unwired command: ignore.
	}
}

// wsWriteFrame writes a single unmasked (server→client) RFC6455 frame: FIN set,
// the given opcode, and payload. It implements the 7-bit, 16-bit (126) and 64-bit
// (127) length forms; the status fragment is a few KB, so the 16-bit path is the
// common one.
func wsWriteFrame(conn net.Conn, opcode byte, payload []byte) error {
	var hdr []byte
	b0 := byte(0x80) | opcode // FIN + opcode
	n := len(payload)
	switch {
	case n < 126:
		hdr = []byte{b0, byte(n)} //nolint:gosec // n<126 fits one byte
	case n < 1<<16:
		hdr = []byte{b0, 126, byte(n >> 8), byte(n)} //nolint:gosec // n<65536: high+low bytes
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n)) //nolint:gosec // len() is non-negative
		hdr = append([]byte{b0, 127}, ext[:]...)
	}
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	_, err := conn.Write(payload)
	return err
}

// wsReadFrame reads a single client→server RFC6455 frame and returns its opcode
// and unmasked payload. Client frames MUST be masked (RFC6455 §5.1); the 4-byte
// masking key is applied to the payload here. The 7-bit and 16-bit length forms
// are handled (a control command is tiny); a 64-bit length is rejected as
// oversized rather than trusted.
func wsReadFrame(conn net.Conn) (opcode byte, payload []byte, err error) {
	h := make([]byte, 2)
	if _, err = io.ReadFull(conn, h); err != nil {
		return 0, nil, err
	}
	opcode = h[0] & 0x0f
	masked := h[1]&0x80 != 0
	n := int(h[1] & 0x7f)
	switch n {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(conn, ext); err != nil {
			return 0, nil, err
		}
		n = int(ext[0])<<8 | int(ext[1])
	case 127:
		return 0, nil, fmt.Errorf("ws frame too large")
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err = io.ReadFull(conn, mask); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(conn, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return opcode, payload, nil
}

// statusSnapshot builds the read-only status.skysocks snapshot. The rich
// per-leg mux/log/event view is visor-side (see
// pkg/visor/embedded_proxystatus.go) because only the visor sees the route
// group; skysocks-client is a separate app process. It is fetched over the app
// RPC (ProxyStatus) and used as the base whenever the visor returned real data.
// The client's OWN truth about the live yamux session to the exit is then
// overlaid on top (Running + stream count). When the RPC is unavailable (the
// browser wasm-visor, or no route group yet) the base is the minimal local
// snapshot, so status.skysocks always renders.
func (c *Client) statusSnapshot() proxystatus.Snapshot {
	snap := c.visorStatusSnapshot()
	if c.anySessionLive() {
		snap.Running = true
		snap.Note = fmt.Sprintf("session to the exit is up · %d open stream(s)", c.totalStreams())
		// With more than one tunnel, surface how many carry the aggregate. A
		// single tunnel keeps the note byte-identical to the pre-aggregation
		// build (no "· N tunnel(s)" suffix).
		if n := len(c.snapshotSessions()); n > 1 {
			snap.Note = fmt.Sprintf("%s · %d tunnels", snap.Note, n)
		}
		// Per-stream detail (id + target + age) behind the count, when tracked.
		snap.Streams = c.streamSnapshot()
	} else {
		snap.Note = "no active session to the exit"
	}
	return snap
}

// visorStatusSnapshot returns the visor-built rich snapshot when the app RPC is
// reachable and carried real data (any Legs/Logs/Events); otherwise the minimal
// local base. Kept separate so the RPC-unavailable path is a clean fallback that
// never breaks the status page.
func (c *Client) visorStatusSnapshot() proxystatus.Snapshot {
	return baseStatusSnapshot(c.appCl)
}

// baseStatusSnapshot builds the status.skysocks base snapshot from the app RPC:
// the visor-built rich snapshot when the RPC is reachable and carried real data
// (any Legs/Logs/Events), otherwise the minimal local base. It carries NO live
// session facts (Running/Note/Streams are overlaid by the caller) so it can be
// shared by the live Client and the sessionless disconnected listener alike.
func baseStatusSnapshot(appCl *app.Client) proxystatus.Snapshot {
	base := proxystatus.Snapshot{
		Surface: proxystatus.SurfaceSkysocks,
		App:     skyenv.SkysocksClientName,
	}
	if appCl == nil {
		return base
	}
	rich, err := appCl.ProxyStatus()
	if err != nil {
		return base
	}
	if len(rich.Legs) == 0 && len(rich.Logs) == 0 && len(rich.Events) == 0 {
		return base
	}
	if rich.Surface == "" {
		rich.Surface = proxystatus.SurfaceSkysocks
	}
	if rich.App == "" {
		rich.App = skyenv.SkysocksClientName
	}
	return rich
}

// statusHTTPResponse wraps the page in a minimal close-delimited HTTP/1.1
// response (mirrors proxystatus.ServeConn's headers, which can't be reused here
// because we write onto the live browser conn rather than an in-memory pipe).
func statusHTTPResponse(body []byte) []byte {
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	b.WriteString("Cache-Control: no-store\r\n")
	b.WriteString("Connection: close\r\n\r\n")
	b.Write(body)
	return b.Bytes()
}

// clearDeadlines removes the sniff read deadlines before the deadline-free data
// splice takes over.
func clearDeadlines(conns ...net.Conn) {
	for _, cn := range conns {
		_ = cn.SetReadDeadline(time.Time{}) //nolint:errcheck
	}
}

func (c *Client) close() {
	if c.appCl != nil {
		c.appCl.Log().Debug("Session failed, closing skysocks client")
	}
	if err := c.Close(); err != nil && c.appCl != nil {
		c.appCl.Log().Errorf("Error closing skysocks client: %v", err)
	}
}

// ListenIPC starts named-pipe based connection server for windows or unix socket for other OSes
func (c *Client) ListenIPC(client *ipc.Client) {
	if c.appCl == nil {
		return
	}
	listenIPC(client, skyenv.SkysocksClientName, c.appCl.Log(), func() {
		client.Close()
		if err := c.Close(); err != nil {
			c.appCl.Log().Errorf("Error closing skysocks-client: %v", err)
		}
	})
}

func (c *Client) setAppError(appErr error) {
	if err := c.appCl.SetError(appErr.Error()); err != nil {
		c.appCl.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	var err error
	c.once.Do(func() {
		if c.appCl != nil {
			c.appCl.Log().Debug("Closing proxy client")
		}

		close(c.closeC)
		// Tear down every tunnel so reconnect builds fresh ones and any
		// in-flight liveness ping unblocks. The first close error (if any) is
		// returned; all sessions are closed regardless.
		for _, s := range c.snapshotSessions() {
			if s == nil {
				continue
			}
			if cerr := s.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	})

	return err
}
