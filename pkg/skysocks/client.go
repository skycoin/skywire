// Package skysocks pkg/skysocks/client.go c4-app-proxy
package skysocks

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // RFC6455 mandates SHA-1 for the WebSocket accept key
	"crypto/x509"
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
	"sync/atomic"
	"time"

	ipc "github.com/0magnet/golang-ipc"
	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
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
// disjoint first-hop transports; the visor steers the extra tunnels onto disjoint
// first-hop transports (#4214), and this Client keeps N of them healthy — a dead
// tunnel is re-dialed as a fresh disjoint replacement (maybeRedial, RFC steps
// 3-4). Throughput-based eviction of a slow-but-alive tunnel is a follow-up.
type Client struct {
	appCl *app.Client
	// sessions holds the live tunnels (>=1). Guarded by sessionsMu because
	// AddTunnel appends while the accept loop / keepalive read concurrently.
	sessions   []*yamux.Session
	sessionsMu sync.Mutex
	// recvStamp maps each tunnel to the wall time (UnixNano) of the last bytes
	// READ from its underlying conn, stamped by the recvStampConn wrapper every
	// tunnel is dialed through. The keepalive loop treats arriving bytes as
	// liveness evidence alongside pongs: under a bulk transfer on a slow leg the
	// pong queues BEHIND the data it shares the conn with, so a pong-only
	// hard-dead window retires the very tunnel that is delivering the download.
	// Guarded by sessionsMu; entries are deleted where lastPong's are.
	recvStamp map[*yamux.Session]*atomic.Int64
	listener  net.Listener
	once      sync.Once
	closeC    chan struct{}

	// streams tracks the currently open tunneled streams so the status page can
	// expand the "N open stream(s)" count into per-stream rows (id + CONNECT
	// target + age, plus this stream's own up/down byte counters and a smoothed
	// transfer rate). The byte counters are metered by a counting wrapper the
	// splice loop reads through (handleStream), so they are true per-stream totals;
	// the rate is an EWMA differenced from them in streamSnapshot at page cadence.
	streamsMu sync.Mutex
	streams   map[uint32]streamMeta

	// Multi-tunnel liveness management (docs/mux_aggregation_rfc.md steps 3-4).
	// target is the desired number of live tunnels (N from --tunnels). When a
	// tunnel dies and the live count falls below target — but at least one tunnel
	// survives — the keepalive loop re-dials a fresh DISJOINT replacement via the
	// redial callback, so aggregation width is restored instead of bleeding down.
	// redial dials one diversify=true tunnel; it is wired FROM the app
	// (cmd/apps/skysocks-client) because the dial lives there, not in this package
	// (SetTunnelRedial). redialInFlight bounds it to a single in-flight re-dial;
	// redialFails backs off after consecutive failures so a persistently
	// unreachable exit is not hammered. All three are guarded by redialMu except
	// redialInFlight, which is its own atomic single-flight guard.
	redialMu       sync.Mutex
	target         int
	redial         func() (net.Conn, error)
	redialFails    int
	redialInFlight atomic.Bool

	// rs configures transparent HTTP range-splitting (see rangesplit.go). A plain
	// GET to a range-capable :80 origin is fetched as N concurrent byte ranges over
	// separate tunnels and reassembled, so one download aggregates across the mesh
	// with no client cooperation. Default-on; the app can retune or disable it.
	rs rangeSplitConfig

	// rsActive/rsSplits/rsChunks/rsBytes are the range-split observability
	// counters the status page surfaces (proxystatus.RangeSplit) so "is
	// range-split firing" is a live field, not just a Debugf. All atomic — the
	// snapshot reads them without locking, like the per-stream meters. rsActive
	// is the in-flight split count (Add(1) on commit, Add(-1) on completion); the
	// rest are monotonic cumulative totals.
	rsActive atomic.Int64
	rsSplits atomic.Uint64
	rsChunks atomic.Uint64
	rsBytes  atomic.Uint64
}

// streamMeta is the per-stream detail the status page surfaces for an open
// tunneled stream. sent/recv are pointers so the map-by-value copy shares the one
// counter the splice loop increments (handleStream wraps the yamux conn in a
// countingConn holding these). The rate/sample fields are mutated only under
// streamsMu in streamSnapshot, which differences the counters at page cadence
// into a smoothed bytes/sec rate.
type streamMeta struct {
	target string
	since  time.Time
	sent   *atomic.Uint64 // cumulative bytes browser→exit (up)
	recv   *atomic.Uint64 // cumulative bytes exit→browser (down)

	// Rate sampling state (guarded by streamsMu, advanced in streamSnapshot).
	lastSent   uint64    // sent counter at the last rate sample
	lastRecv   uint64    // recv counter at the last rate sample
	lastSample time.Time // wall time of the last rate sample
	upRate     float64   // smoothed up rate, bytes/sec (EWMA)
	downRate   float64   // smoothed down rate, bytes/sec (EWMA)
}

// countingConn wraps a net.Conn to meter the bytes flowing each way through it.
// rd counts bytes READ from the conn, wr counts bytes WRITTEN to it; both are
// best-effort (a partial read/write still credits what moved). Used to meter a
// yamux exit stream: reads are exit→browser (down/recv), writes are browser→exit
// (up/sent). All other net.Conn methods pass through unchanged.
type countingConn struct {
	net.Conn
	rd *atomic.Uint64
	wr *atomic.Uint64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.rd.Add(uint64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.wr.Add(uint64(n))
	}
	return n, err
}

// errAllTunnelsDown is the synthetic stream-open error used when every tunnel
// is closed so pickSession returns nil and there is no live session to Open() a
// real error from. It drives the same route-down interstitial + reconnect path a
// single closed session took before.
var errAllTunnelsDown = errors.New("all tunnels to the exit are down")

// recvStampConn wraps a net.Conn to record the wall time of every successful
// read. The keepalive loop reads the stamp as proof the remote is still
// sending, so a tunnel mid-download is never retired just because its pong is
// queued behind the download's own frames (both ride the same conn).
type recvStampConn struct {
	net.Conn
	stamp *atomic.Int64
}

func (c *recvStampConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.stamp.Store(time.Now().UnixNano())
	}
	return n, err
}

// newYamuxSession wraps a dialed route-group conn in a yamux client session with
// skysocks's flow-control window, metering inbound bytes into a receive-time
// stamp for the keepalive loop. Shared by NewClient and AddTunnel so every
// tunnel is configured identically.
func newYamuxSession(conn net.Conn) (*yamux.Session, *atomic.Int64, error) {
	stamp := new(atomic.Int64)
	sessionCfg := yamux.DefaultConfig()
	sessionCfg.EnableKeepAlive = false
	sessionCfg.MaxStreamWindowSize = muxStreamWindowBytes
	session, err := yamux.Client(&recvStampConn{Conn: conn, stamp: stamp}, sessionCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating client: yamux: %w", err)
	}
	return session, stamp, nil
}

// NewClient constructs a new single-tunnel Client. Signature unchanged: this is
// the common case and every existing caller/test builds one tunnel this way. Use
// AddTunnel (or NewMultiClient) to stripe browser connections across N tunnels.
func NewClient(conn net.Conn, appCl *app.Client) (*Client, error) {
	c := &Client{
		appCl:   appCl,
		closeC:  make(chan struct{}),
		streams: make(map[uint32]streamMeta),
		// One tunnel is the default target: with N==1 the sole tunnel's death is
		// total collapse (handled by the app's --reconnect), so N==1 never
		// re-dials — byte-identical to the pre-aggregation build. NewMultiClient
		// raises this to the conn count, and the app sets --tunnels via
		// SetTunnelTarget so a re-dial can refill even after a short initial dial.
		target: 1,
		rs:     defaultRangeSplitConfig(),
	}

	session, stamp, err := newYamuxSession(conn)
	if err != nil {
		return nil, err
	}
	c.sessions = []*yamux.Session{session}
	c.recvStamp = map[*yamux.Session]*atomic.Int64{session: stamp}

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
	// Target the number of tunnels we actually built. The app overrides this
	// with the requested --tunnels via SetTunnelTarget so a re-dial refills to
	// the full width even when some initial dials fell short.
	c.SetTunnelTarget(len(conns))
	return c, nil
}

// AddTunnel wraps an additional dialed route-group conn in a yamux session and
// appends it to the tunnel set. The shared keepalive loop and pickSession pick it
// up automatically. This is the extension point the disjoint-dial coordinator
// (RFC step 3) will call to grow the tunnel set at runtime.
func (c *Client) AddTunnel(conn net.Conn) error {
	session, stamp, err := newYamuxSession(conn)
	if err != nil {
		return err
	}
	c.sessionsMu.Lock()
	c.sessions = append(c.sessions, session)
	if c.recvStamp == nil {
		c.recvStamp = make(map[*yamux.Session]*atomic.Int64)
	}
	c.recvStamp[session] = stamp
	c.sessionsMu.Unlock()
	return nil
}

// SetTunnelTarget sets the desired number of live tunnels N. When a tunnel dies
// and the live count falls below N (but at least one tunnel survives), the
// keepalive loop re-dials a replacement via the SetTunnelRedial callback. The app
// sets this to --tunnels so a re-dial restores the full aggregation width even if
// some initial dials fell short. Values < 1 are clamped to 1 — a single tunnel
// never re-dials (its death is total collapse, owned by the app's --reconnect).
func (c *Client) SetTunnelTarget(n int) {
	if n < 1 {
		n = 1
	}
	c.redialMu.Lock()
	c.target = n
	c.redialMu.Unlock()
}

// SetRangeSplit configures transparent HTTP range-splitting. concurrency<1 or
// chunkSize<1 keep the current value; enabled=false disables the feature entirely
// (every request splices through unchanged). The app wires this from its flags so
// the capability is default-on but tunable, not gated behind a required flag.
func (c *Client) SetRangeSplit(enabled bool, concurrency int, chunkSize int64) {
	c.rs.enabled = enabled
	if concurrency >= 1 {
		c.rs.concurrency = concurrency
	}
	if chunkSize >= 1 {
		c.rs.chunkSize = chunkSize
	}
}

// SetHTTPSRangeSplitMinter enables TLS-terminating (:443) range-splitting on this
// client using a MITM root + minter the CALLER already created (via
// LoadOrCreateMITMCA). This is the production path: the CA is a persistent local
// identity minted ONCE at app startup — independent of any dial — and the same
// minter is injected into every reconnect's client, so the operator can import the
// cert before traffic ever flows and it never changes underfoot.
func (c *Client) SetHTTPSRangeSplitMinter(cert *x509.Certificate, minter skynetca.LeafMinter) {
	if cert == nil || minter == nil {
		return
	}
	c.rs.caCert = cert
	c.rs.minter = minter
	c.rs.httpsEnabled = true
}

// SetHTTPSRangeSplit is a convenience that loads/creates the MITM root under caDir
// and enables the feature on this client in one call. Prefer LoadOrCreateMITMCA +
// SetHTTPSRangeSplitMinter when the CA must exist before the first client is built
// (so it can be exported up front). Errors (unreadable/creatable CA) leave the
// feature off.
func (c *Client) SetHTTPSRangeSplit(caDir string) error {
	cert, minter, err := LoadOrCreateMITMCA(caDir)
	if err != nil {
		return err
	}
	c.SetHTTPSRangeSplitMinter(cert, minter)
	return nil
}

// SetHTTPSRangeSplitOriginRoots overrides the roots used to verify the REAL origin's
// certificate (nil = system roots). Intended for tests that stand up an httptest TLS
// origin; production leaves it nil so origin security is never downgraded.
func (c *Client) SetHTTPSRangeSplitOriginRoots(pool *x509.CertPool) { c.rs.originRoots = pool }

// MITMCACertPEM returns the PEM of the HTTPS range-split MITM root for the operator
// to import into a browser. ok is false when HTTPS range-splitting is not configured.
func (c *Client) MITMCACertPEM() ([]byte, bool) { return c.mitmCACertPEM() }

// SetTunnelRedial wires the app's disjoint-diversify dial into the Client so the
// keepalive loop can re-dial a dead tunnel's replacement. The dial itself lives in
// the app (cmd/apps/skysocks-client) — it needs the server PK, the retrier and the
// appnet fallback machinery the Client has no handle on — so the app closes over
// its dialServer(...,diversify=true) call and hands it here. fn must return a fresh
// dialed route-group conn (the same kind NewMultiClient wraps); the loop wraps it
// in a yamux session via AddTunnel. A nil fn (the default, and the single-tunnel
// case) disables re-dial entirely.
func (c *Client) SetTunnelRedial(fn func() (net.Conn, error)) {
	c.redialMu.Lock()
	c.redial = fn
	c.redialMu.Unlock()
}

// liveSessionCount returns the number of tunnels still up.
func (c *Client) liveSessionCount() int {
	n := 0
	for _, s := range c.snapshotSessions() {
		if s != nil && !s.IsClosed() {
			n++
		}
	}
	return n
}

// resetRedialBackoff clears the consecutive-failure counter so re-dial is armed
// again. Called when a FRESH tunnel death is observed (the live count dropped), so
// an exit that had gone quiet is retried once it loses another tunnel rather than
// staying permanently backed off.
func (c *Client) resetRedialBackoff() {
	c.redialMu.Lock()
	c.redialFails = 0
	c.redialMu.Unlock()
}

// maxRedialFails bounds consecutive failed re-dials before the keepalive loop
// stops re-trying until the NEXT tunnel death re-arms it (resetRedialBackoff).
// Without this a persistently-unreachable exit would spin a re-dial on every
// liveness tick forever.
const maxRedialFails = 3

// maybeRedial re-dials ONE replacement tunnel when the live count (passed in, to
// avoid re-snapshotting) has fallen below the target N — restoring aggregation
// width after a tunnel dies. It is a no-op unless a re-dial callback is wired
// (SetTunnelRedial) and:
//
//   - never re-dials once EVERY tunnel is closed (live <= 0): that is total
//     collapse, which the app's outer --reconnect runCycle owns — re-dialing here
//     would duplicate it and fight the whole-client rebuild. With a single tunnel
//     (N==1) its death is the only death, so N==1 never re-dials: byte-identical
//     to the pre-aggregation build.
//   - bounds itself to a SINGLE in-flight re-dial (redialInFlight, an atomic CAS
//     mirroring the router's healInFlight guard) so a burst of deaths cannot fan
//     out a storm of concurrent dials.
//   - backs off after maxRedialFails consecutive failures until the next death.
//
// The replacement uses the SAME diversify=true dial as the initial extra tunnels
// (the callback closes over dialServer), so it steers onto a first-hop transport
// the survivors don't already occupy (#4214). AddTunnel appends it under the
// sessions mutex; the shared keepalive loop and pickSession then pick it up.
//
// Throughput-based eviction (retiring a slow-but-ALIVE tunnel to cycle in a
// faster disjoint one — the "drop the underperforming" half of RFC step 4) is a
// deliberate follow-up: it needs the gigabit validation rig to tune the
// slow-leg threshold, and mis-tuned it would thrash healthy tunnels. This method
// is liveness-only: a DEAD tunnel is replaced; a live one is left alone.
func (c *Client) maybeRedial(live int) {
	c.redialMu.Lock()
	fn := c.redial
	target := c.target
	if target < 1 {
		target = 1
	}
	backedOff := c.redialFails >= maxRedialFails
	c.redialMu.Unlock()

	if fn == nil || backedOff || live >= target || live <= 0 {
		return
	}
	if !c.redialInFlight.CompareAndSwap(false, true) {
		return // a re-dial is already running
	}
	go func() {
		defer c.redialInFlight.Store(false)
		conn, err := fn()
		if err != nil {
			c.redialMu.Lock()
			c.redialFails++
			fails := c.redialFails
			c.redialMu.Unlock()
			if c.appCl != nil {
				if fails >= maxRedialFails {
					c.appCl.Log().Warnf("Tunnel re-dial failed (%d/%d consecutive); backing off until the next tunnel death: %v", fails, maxRedialFails, err)
				} else {
					c.appCl.Log().Warnf("Tunnel re-dial failed (%d/%d); retrying next tick: %v", fails, maxRedialFails, err)
				}
			}
			return
		}
		if aerr := c.AddTunnel(conn); aerr != nil {
			_ = conn.Close() //nolint:errcheck,gosec
			c.redialMu.Lock()
			c.redialFails++
			c.redialMu.Unlock()
			if c.appCl != nil {
				c.appCl.Log().Warnf("Tunnel re-dial connected but wrapping it failed: %v", aerr)
			}
			return
		}
		c.redialMu.Lock()
		c.redialFails = 0
		c.redialMu.Unlock()
		if c.appCl != nil {
			c.appCl.Log().Infof("Re-dialed a replacement tunnel; %d live tunnel(s) toward target %d", c.liveSessionCount(), target)
		}
	}()
}

// snapshotSessions returns a copy of the current tunnel set for lock-free
// iteration by callers (keepalive, status).
// lastRecvTime returns the wall time of the last bytes read from s's underlying
// conn (zero time if never / unknown). Read by the keepalive loop as liveness
// evidence alongside pongs.
func (c *Client) lastRecvTime(s *yamux.Session) time.Time {
	c.sessionsMu.Lock()
	stamp := c.recvStamp[s]
	c.sessionsMu.Unlock()
	if stamp == nil {
		return time.Time{}
	}
	if ns := stamp.Load(); ns > 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// dropRecvStamp forgets a retired session's receive stamp (map hygiene mirroring
// the keepalive loop's lastPong deletes).
func (c *Client) dropRecvStamp(s *yamux.Session) {
	c.sessionsMu.Lock()
	delete(c.recvStamp, s)
	c.sessionsMu.Unlock()
}

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
	l, err := ReuseListen(addr)
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
// never flip and ListenAndServe would block forever in Accept(). A yamux
// ping detects that: the loop retires a tunnel only after no pong has been
// seen for sessionHardDeadWindow, which tolerates a merely-slow or
// transiently reorder-wedged route (a false close costs a reconnect cycle).
//
// livenessProbeInterval is how often the keepalive loop probes each tunnel. A
// var so tests can drive the loop fast.
var livenessProbeInterval = 15 * time.Second

// sessionHardDeadWindow is how long a tunnel may go WITHOUT any pong before the
// keepalive loop retires it as dead. It is deliberately much larger than
// livenessProbeInterval: the yamux ping/pong are ordinary frames on the
// RouteGroup, which is a reliable ORDERED stream, so a reorder WEDGE (a missing
// sequence damming later packets — including the pong — until the sender's
// retransmit refills it) head-of-line-blocks the pong for as long as the wedge
// lasts. A single stuck probe therefore does NOT mean the tunnel is dead; the
// pong still arrives once the wedge clears. Retiring on a couple of stuck probes
// (the old livenessFailThreshold logic) mistook a transient wedge for a dead
// conn and tore down the whole route group under download load — the pool
// "collapse to nothing, then grow back" (new local port on every reconnect). We
// now retire only after NO pong (even a late one) AND no inbound bytes have
// been seen for this window, which distinguishes a wedged-but-live tunnel
// (pongs arrive late, or the download's own bytes keep arriving while the pong
// sits behind them) from a genuinely silent/black-holed one (never sends
// again). A var so tests can shrink it. Kept comfortably above the worst
// realistic wedge duration.
var sessionHardDeadWindow = 45 * time.Second

// sessionKeepAliveLoop probes every tunnel and retires only the genuinely dead
// ones. Each tick it issues at most one in-flight yamux ping per tunnel; the
// ping's result — INCLUDING one that arrives late, after its own probe timed
// out — refreshes that tunnel's "last pong" timestamp. A tunnel is retired only
// once NO pong (early or late) has been seen for sessionHardDeadWindow. This is
// the fix for the false-teardown collapse: a reorder WEDGE head-of-line-blocks
// the in-band pong for a few seconds (it rides the same reliable ordered stream
// as the dammed data), so the old "2 consecutive stuck pings → retire" logic
// mistook a transient wedge for a dead conn and tore the whole route group down
// under download load. Tracking the late pong distinguishes wedged-but-live
// (pong arrives once the gap fills) from silent/black-holed (never pongs).
//
// Retiring a tunnel closes it so pickSession stops routing to it; the whole
// client is torn down for reconnect only once EVERY tunnel is closed. With a
// single tunnel (N==1) the one tunnel dying closes the client (its death is
// total collapse, owned by the app's --reconnect). After retiring, when the live
// count has fallen below the target N (but at least one tunnel survives) it
// re-dials a fresh DISJOINT replacement via maybeRedial (docs/mux_aggregation_rfc.md
// steps 3-4).
func (c *Client) sessionKeepAliveLoop() {
	ticker := time.NewTicker(livenessProbeInterval)
	defer ticker.Stop()

	type probeResult struct {
		s  *yamux.Session
		ok bool
	}
	// resC carries every ping's outcome back to the loop, even outcomes that
	// arrive long after livenessProbeTimeout (a wedge-delayed pong). Buffered so a
	// late result never blocks its goroutine when the loop is between ticks.
	resC := make(chan probeResult, 64)
	lastPong := make(map[*yamux.Session]time.Time) // last time a pong was seen (early or late)
	inFlight := make(map[*yamux.Session]bool)      // a ping is outstanding for this session
	prevLive := -1

	for {
		select {
		case <-c.closeC:
			return
		case r := <-resC:
			// A ping completed (possibly late). Clear its in-flight guard and, if it
			// ponged, mark the tunnel alive as of now — this is what lets a wedge that
			// clears after the probe deadline keep the tunnel from being retired.
			inFlight[r.s] = false
			if r.ok {
				lastPong[r.s] = time.Now()
			}
		case <-ticker.C:
			now := time.Now()
			for _, s := range c.snapshotSessions() {
				if s.IsClosed() {
					delete(lastPong, s)
					delete(inFlight, s)
					c.dropRecvStamp(s)
					continue
				}
				if _, seen := lastPong[s]; !seen {
					lastPong[s] = now // seed on first sight so a never-ponging conn still ages out
				}
				// Issue a fresh probe only if the previous one has resolved. A ping
				// wedged behind a reorder gap keeps its goroutine parked until the gap
				// fills (or the session closes), so we do not pile up probes; the single
				// outstanding ping's eventual pong refreshes liveness via resC.
				if !inFlight[s] {
					inFlight[s] = true
					go func(s *yamux.Session) {
						_, err := s.Ping()
						select {
						case resC <- probeResult{s: s, ok: err == nil}:
						case <-c.closeC:
						}
					}(s)
				}
				// Retire only after a sustained TOTAL silence — no pong AND no bytes
				// read from the conn for the whole window. Arriving bytes are liveness
				// evidence in their own right: under a bulk transfer on a slow leg the
				// pong queues behind the transfer's own frames, and a pong-only window
				// would retire the very tunnel delivering the download.
				lastAlive := lastPong[s]
				if rt := c.lastRecvTime(s); rt.After(lastAlive) {
					lastAlive = rt
				}
				if now.Sub(lastAlive) >= sessionHardDeadWindow {
					if c.appCl != nil {
						c.appCl.Log().Warnf("No pong and no inbound bytes for %v (> hard-dead window); tunnel gone, retiring it", now.Sub(lastAlive).Truncate(time.Second))
					}
					_ = s.Close() //nolint:errcheck
					delete(lastPong, s)
					delete(inFlight, s)
					c.dropRecvStamp(s)
				}
			}
			if c.allSessionsClosed() {
				c.close()

				return
			}
			// A FRESH death (live count dropped since the last tick) re-arms the
			// re-dial backoff, so an exit that had gone quiet is retried once it
			// loses another tunnel instead of staying permanently backed off.
			live := c.liveSessionCount()
			if prevLive >= 0 && live < prevLive {
				c.resetRedialBackoff()
			}
			prevLive = live
			c.maybeRedial(live)
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
	// age + up/down bytes). Registered here, deregistered when the splice below
	// returns. The returned counters are wired into a countingConn wrapping the
	// yamux stream so every byte the splice/range-split loop moves is metered:
	// reads (exit→browser) credit recv/down, writes (browser→exit) credit
	// sent/up. Wrapping the stream itself catches both the plain splice and the
	// range-split path, which read/write this same conn.
	if id, ok := streamID(stream); ok {
		up, down := c.addStream(id, target)
		stream = &countingConn{Conn: stream, rd: down, wr: up}
		defer c.removeStream(id)
	}

	// Transparent HTTP range-splitting: a plain GET to a range-capable :80 origin is
	// fetched as N concurrent byte ranges over separate tunnels and reassembled, so
	// one unmodified download aggregates across the mesh. serveHTTPRangeSplit takes
	// ownership of both ends (splicing through byte-for-byte for anything it cannot
	// split). Everything else — HTTPS, non-80 ports, feature disabled — splices as
	// before.
	if c.rs.enabled && isPort80(target) {
		c.serveHTTPRangeSplit(conn, stream)
	} else if c.rs.httpsEnabled && c.rs.minter != nil && isPort443(target) {
		// Opt-in TLS-terminating split (rangesplit_https.go). Only reached when the
		// operator enabled it and a minter exists; a plain :443 splices as before.
		c.serveHTTPSRangeSplit(conn, stream, target)
	} else {
		c.splicePrefixed(conn, stream, nil)
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

// rateSampleMin is the minimum wall interval between two rate samples for a
// stream: below it the delta is too small to divide cleanly, so streamSnapshot
// keeps the last smoothed rate instead of recomputing. The status page pushes at
// ~1s, comfortably above this floor.
const rateSampleMin = 400 * time.Millisecond

// rateEWMAAlpha weights the newest instantaneous sample against the running
// smoothed rate (0.5 = equal), so a burst shows promptly but single-sample noise
// is damped — matching the ~1s page cadence.
const rateEWMAAlpha = 0.5

// representativeRouteRTT picks a single route-group latency to attach to the
// per-stream rows: the minimum end-to-end route RTT among the alive legs (the
// fastest live path the session can use), falling back to the min first-hop
// transport RTT when no leg reports a route RTT. Returns 0 when nothing is
// measured. Route-group-level, not per-stream — the streams stripe across these
// legs and yamux exposes no per-stream RTT.
func representativeRouteRTT(legs []proxystatus.Leg) float64 {
	best := 0.0
	for _, l := range legs {
		if !l.Alive {
			continue
		}
		cand := l.RouteLatencyMS
		if cand <= 0 {
			cand = l.LatencyMS
		}
		if cand <= 0 {
			continue
		}
		if best == 0 || cand < best {
			best = cand
		}
	}
	return best
}

// addStream/removeStream/streamSnapshot maintain the open-stream registry the
// status page reads. addStream returns the up/down byte counters for the new
// stream so the caller can wire them into a countingConn on the yamux stream;
// the splice loop then meters every byte through them.
func (c *Client) addStream(id uint32, target string) (up, down *atomic.Uint64) {
	up, down = new(atomic.Uint64), new(atomic.Uint64)
	now := time.Now()
	c.streamsMu.Lock()
	if c.streams == nil {
		c.streams = make(map[uint32]streamMeta)
	}
	c.streams[id] = streamMeta{target: target, since: now, sent: up, recv: down, lastSample: now}
	c.streamsMu.Unlock()
	return up, down
}

func (c *Client) removeStream(id uint32) {
	c.streamsMu.Lock()
	delete(c.streams, id)
	c.streamsMu.Unlock()
}

// rangeSplitSnapshot reports the live range-split summary from the atomic
// counters. It returns nil when range-splitting is disabled (so the status page
// simply omits the section); when enabled it always reports, so ActiveSplits==0
// with zero totals reads as "on, nothing splitting yet".
func (c *Client) rangeSplitSnapshot() *proxystatus.RangeSplit {
	if !c.rs.enabled {
		return nil
	}
	return &proxystatus.RangeSplit{
		Enabled:         true,
		ActiveSplits:    c.rsActive.Load(),
		TotalSplits:     c.rsSplits.Load(),
		TotalChunks:     c.rsChunks.Load(),
		TotalBytes:      c.rsBytes.Load(),
		StreamsPerSplit: c.rs.concurrency,
		ChunkSize:       c.rs.chunkSize,
	}
}

// streamSnapshot returns the currently open streams as sorted proxystatus.Stream
// rows (by id) for the status page, carrying each stream's cumulative up/down
// bytes and a smoothed up/down rate. The rate is an EWMA differenced from the
// cumulative counters against the previous sample; when called faster than
// rateSampleMin it reports the last smoothed rate without advancing the sample,
// so a fast redraw does not divide a tiny delta by a tiny interval. Rate state is
// mutated in place under streamsMu (the map holds streamMeta by value, so each
// updated meta is written back).
func (c *Client) streamSnapshot() []proxystatus.Stream {
	c.streamsMu.Lock()
	defer c.streamsMu.Unlock()
	if len(c.streams) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]proxystatus.Stream, 0, len(c.streams))
	for id, m := range c.streams {
		var curSent, curRecv uint64
		if m.sent != nil {
			curSent = m.sent.Load()
		}
		if m.recv != nil {
			curRecv = m.recv.Load()
		}
		if dt := now.Sub(m.lastSample).Seconds(); dt >= rateSampleMin.Seconds() {
			upInst := float64(curSent-m.lastSent) / dt
			downInst := float64(curRecv-m.lastRecv) / dt
			if m.lastSent == 0 && m.lastRecv == 0 && m.upRate == 0 && m.downRate == 0 {
				// First sample: seed directly so the first shown rate is the real
				// average over the stream's opening interval, not half of it.
				m.upRate, m.downRate = upInst, downInst
			} else {
				m.upRate = rateEWMAAlpha*upInst + (1-rateEWMAAlpha)*m.upRate
				m.downRate = rateEWMAAlpha*downInst + (1-rateEWMAAlpha)*m.downRate
			}
			m.lastSent, m.lastRecv, m.lastSample = curSent, curRecv, now
			c.streams[id] = m
		}
		out = append(out, proxystatus.Stream{
			ID:          id,
			Target:      m.target,
			AgeMS:       now.Sub(m.since).Milliseconds(),
			SentBytes:   curSent,
			RecvBytes:   curRecv,
			SentRateBps: m.upRate,
			RecvRateBps: m.downRate,
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

	switch statusRequestPath(buf[:n]) {
	case "/ws":
		// status is served entirely in-process, so the exit-side yamux stream is
		// unused — close it now so the long-lived WS loop doesn't pin one open.
		stream.Close() //nolint:errcheck,gosec
		if !wsHandshake(conn, buf[:n]) {
			return
		}
		c.serveStatusWS(conn)
		return
	case "/main.wasm":
		// The GPU route-graph view's engine: the one wasm-visor blob run in its
		// "netview" role (it publishes the generic cosmos-go graph API the page
		// drives), served same-origin so the page's strict self-contained context
		// can instantiate it. Same blob pkg/tpviz serves at /tpviz-gl.wasm; served
		// here straight from pkg/wasmhv/wasmbin. In-process, no exit round-trip.
		stream.Close()                          //nolint:errcheck,gosec
		_, _ = conn.Write(statusWasmResponse()) //nolint:errcheck
		return
	case "/wasm_exec.js":
		stream.Close()                              //nolint:errcheck,gosec
		_, _ = conn.Write(statusWasmExecResponse()) //nolint:errcheck
		return
	}

	body := proxystatus.Render(c.statusSnapshot())
	_, _ = conn.Write(statusHTTPResponse(body)) //nolint:errcheck
}

// statusWasmVariant picks which embedded wasm-visor variant backs the route-graph
// view, preferring the smaller TinyGo blob when present (a ~3 MB download vs the
// ~9.5 MB standard-Go one). Mirrors pkg/tpviz's tpvizNetviewVariant. The second
// return is false when no blob is embedded, in which case the page silently
// falls back to the ASCII tree view.
func statusWasmVariant() (wasmbin.Variant, bool) {
	switch {
	case !wasmbin.Embedded():
		return wasmbin.Default(), false
	case wasmbin.Has(wasmbin.TinyGo):
		return wasmbin.TinyGo, true
	default:
		return wasmbin.Default(), true
	}
}

// statusWasmResponse returns the raw HTTP/1.1 response carrying the wasm-visor
// blob for /main.wasm. It serves the gzip-committed bytes verbatim with
// Content-Encoding: gzip (the browser inflates; WebAssembly.instantiateStreaming
// is happy with the result), avoiding inflating megabytes per request. A 503 is
// returned when no blob is embedded.
func statusWasmResponse() []byte {
	v, ok := statusWasmVariant()
	if !ok {
		return statusServiceUnavailable("no wasm-visor blob embedded in this build")
	}
	gz := wasmbin.GetVariantGz(v)
	if len(gz) == 0 {
		return statusServiceUnavailable("wasm-visor blob unavailable")
	}
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: application/wasm\r\n")
	b.WriteString("Content-Encoding: gzip\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(gz))
	b.WriteString("Cache-Control: no-store\r\nConnection: close\r\n\r\n")
	b.Write(gz)
	return b.Bytes()
}

// statusWasmExecResponse returns the raw HTTP/1.1 response for /wasm_exec.js —
// the loader that matches the served blob's toolchain (a TinyGo blob needs
// TinyGo's loader). Small, so served uncompressed.
func statusWasmExecResponse() []byte {
	v, ok := statusWasmVariant()
	if !ok {
		return statusServiceUnavailable("no wasm-visor blob embedded in this build")
	}
	js := wasmbin.WasmExecJSVariant(v)
	if len(js) == 0 {
		return statusServiceUnavailable("wasm loader unavailable")
	}
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: application/javascript; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(js))
	b.WriteString("Cache-Control: no-store\r\nConnection: close\r\n\r\n")
	b.Write(js)
	return b.Bytes()
}

// statusServiceUnavailable builds a tiny 503 response (the page treats a failed
// wasm fetch as "graph unavailable" and keeps the tree view).
func statusServiceUnavailable(msg string) []byte {
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 503 Service Unavailable\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(msg))
	b.WriteString("Cache-Control: no-store\r\nConnection: close\r\n\r\n")
	b.WriteString(msg)
	return b.Bytes()
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
		// Per-stream detail (id + target + age + up/down bytes/rate) behind the
		// count, when tracked. yamux exposes no per-stream RTT, so each stream is
		// tagged with the route-group latency (the session's representative route
		// RTT to the exit) rather than a faked per-stream number; the renderer
		// labels the column as route-group latency.
		snap.Streams = c.streamSnapshot()
		if rgRTT := representativeRouteRTT(snap.Legs); rgRTT > 0 {
			for i := range snap.Streams {
				snap.Streams[i].LatencyMS = rgRTT
			}
		}
	} else {
		snap.Note = "no active session to the exit"
	}
	// Range-split summary is local client truth (the counters live here, not in
	// the visor-built base), overlaid like Streams so status.skysocks shows
	// whether transparent HTTP range-splitting is firing right now.
	snap.RangeSplit = c.rangeSplitSnapshot()
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
