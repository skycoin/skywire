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
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ipc "github.com/0magnet/golang-ipc"
	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
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

// muxConnWriteTimeout replaces yamux's 10s ConnectionWriteTimeout "safety
// valve" on both the client and server sessions. The valve exists to bound a
// write into a silently dead conn — but on a skysocks tunnel liveness is
// already owned by the client's keepalive loop (sessionHardDeadWindow, 45s of
// total silence), and a genuinely dead session unblocks pending writes with
// ErrSessionShutdown the moment it is retired. At 10s the valve fired on
// HEALTHY sessions: a saturated upload backs the session send queue up behind
// the mesh's actual drain rate, a stream Write blocks past 10s, and the
// splice tears the connection down — measured live as a reproducible RST ~20s
// into every bulk upload (and the server side does the same to saturated
// downloads). Sized above sessionHardDeadWindow so the keepalive verdict,
// not the valve, decides death.
const muxConnWriteTimeout = 60 * time.Second

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
	// The same meter counts the tunnel's bytes each way and remembers the
	// capacity it has shown, which pickSession weighs a new stream by.
	recvStamp map[*yamux.Session]*tunnelMeter
	listener  net.Listener
	once      sync.Once
	closeC    chan struct{}
	// keepAliveDone is closed when sessionKeepAliveLoop returns, so a caller can
	// join the loop before tearing down what it depends on.
	keepAliveDone chan struct{}

	// probeInterval and hardDeadWindow are this client's copies of the
	// package-level liveness tunables (see livenessProbeInterval and
	// sessionHardDeadWindow), snapshotted by NewClient BEFORE the keepalive
	// goroutine starts and never written afterwards. The loop reads only these.
	//
	// They are per-client rather than global because the loop outlives the call
	// that created it: NewClient spawns the goroutine, and a caller that closes
	// the client without joining keepAliveDone leaves it running a while longer.
	// In tests that meant one test's leaked loop was still reading the globals
	// while the next test's harness shrank them — a genuine data race, and one
	// that care inside any single test could not fix, because the racing ends
	// belong to different tests. Copying per client removes the sharing rather
	// than synchronizing it.
	probeInterval  time.Duration
	hardDeadWindow time.Duration
	// probeFailWin is the same kind of snapshot for sessionProbeFailWindow:
	// how long a ping may stay unanswered before it counts as a failed probe.
	// See tunnel_liveness.go — silence alone no longer retires a tunnel.
	probeFailWin time.Duration

	// exitOpenTimeouts is the client's cumulative count of exit opens that timed
	// out — a stream whose exit never answered the SOCKS5 greeting inside
	// statusSniffTimeout, which returns the browser nothing at all. Kept here as
	// well as per-tunnel because a retired tunnel's meter is dropped, and the
	// status page's number must not go backwards. Surfaced as
	// proxystatus.Snapshot.ExitOpenTimeouts.
	exitOpenTimeouts atomic.Uint64

	// sniffTimeout overrides statusSniffTimeout for this client's exit opens.
	// Zero (the only value production sets) means statusSniffTimeout. It is a
	// per-client field rather than a package var because the package vars the
	// keepalive loop used to read raced across tests; see probeInterval above.
	sniffTimeout time.Duration

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

	// spreadLast holds the *spreadPlanner of the most recently COMPLETED split
	// download or striped upload — the per-tunnel byte ledger behind the
	// `shares=` line of its completion log (spread.go). One value, replaced
	// wholesale, so a reader always sees one object's shares and never a mix
	// of two.
	spreadLast atomic.Value

	// standby marks the tunnels held in the POOL rather than carrying streams.
	// Guarded by sessionsMu, keyed like recvStamp; absent means active. A
	// standby tunnel is a fully dialed route group + noise + yamux session to
	// the exit with zero user streams: it is pinged on the same 5 s cadence,
	// so its RTT stays fresh, and the picker skips it exactly the way it skips
	// a benched tunnel — unless nothing else is live, in which case a standby
	// tunnel is far better than refusing to pick.
	//
	// The point is time. A fresh tunnel costs a setup-node round of ~8-9 s; a
	// held one costs a map lookup. See poolDial for how the set is grown.
	standby map[*yamux.Session]bool

	// Standby pool fill (all guarded by redialMu except poolFillInFlight,
	// which is its own single-flight guard, mirroring redialInFlight).
	//
	// poolDial dials ONE more sibling tunnel that REQUIRES a first hop no
	// tunnel already held occupies; the app wires it (SetPoolDial) because the
	// dial lives there. poolMax caps the tunnels held INCLUDING the active
	// ones, so the setup-node load of a fill is bounded; 0 disables the pool.
	//
	// poolArmed is what keeps this from becoming the self-heal storm of #4325.
	// The fill runs only while armed; it disarms itself the moment the router
	// says no disjoint first hop is left (poolSettledAt / poolSettledN record
	// where it rested and poolSettledLogged keeps that one line to one line),
	// and is re-armed ONLY by a tunnel death. Sizing is discovered, never
	// chased: an exit reachable over three disjoint first hops settles at
	// three and stops dialing, whatever poolMax says.
	//
	// A DIAL FAILURE is not exhaustion and must not rest the fill for good:
	// measured on the rig 2026-09-17, three pool tunnels died, their three
	// re-dials failed, and the pool stayed parked at five while the topology
	// still had free first hops. poolRetryAt / poolRetryRound give failures a
	// BOUNDED second look — poolRetryRounds rounds of maxRedialFails dials,
	// each round behind a longer backoff — so the fill can still reach the
	// real bound without ever becoming a loop. A dial that lands clears both.
	poolDial          func() (net.Conn, error)
	poolMax           int
	poolArmed         bool
	poolFails         int
	poolRetryAt       time.Time
	poolRetryRound    int
	poolSettledAt     time.Time
	poolSettledN      int
	poolSettledReason string
	poolSettledLogged bool
	// poolFillInFlight is how many pool dials are running right now, bounded by
	// router.SetupFillInflight(). It used to be a single-flight bool, which made
	// the fill strictly serial: one dial per keepalive tick, each paying its own
	// route-finder/oracle query and its own setup-node request, so a pool of
	// eight took ~56 s to build. Concurrent dials are also what gives the
	// initiator-side batcher something to coalesce — they arrive at the setup
	// dialer within milliseconds of each other and leave as ONE batched request
	// (pkg/router/setup_batch_client.go).
	poolFillInFlight atomic.Int32

	// muxNote reports one tunnel switch to the visor, which records it on the
	// router's mux-event ring and re-labels the route group's tunnel role. Set
	// by NewClient from the app client; nil (every unit test, and a Client
	// built without an app) simply means nothing is reported.
	//
	// The calls go through muxNoteC and one drain goroutine rather than
	// straight out of the keepalive loop, because this is an RPC to the visor
	// over the same proc conn a dial uses: a slow visor must not be able to
	// stall the loop that retires dead tunnels. The channel keeps the ORDER
	// (retired before promoted, which is how a failover reads) and drops on a
	// full buffer rather than blocking — an unreported event is a missing
	// line in `visor state`, a blocked keepalive loop is an outage.
	muxNote  func(port routing.Port, event, reason, role string) error
	muxNoteC chan tunnelNote

	// appSettings PULLS the live tuning knobs the visor holds for this app.
	// Indirected like muxNote so a test can drive it without an app RPC, and
	// nil on any build with no visor to ask (wasm, unit tests) — pullSettings
	// is then a no-op and every knob keeps its compiled default.
	// settingsApplied is the version last installed, reported back on each pull
	// so the visor can answer nothing when nothing moved; it is touched only by
	// the keepalive loop.
	// The list knobs arrive in their own map and the edge-triggered ops
	// (cut-tunnel) beside both; opsApplied is the highest op sequence carried
	// out, reported back so an op leaves the visor's queue exactly once.
	appSettings     func(applied, opsApplied uint64) (map[string]int64, map[string]string, []appserver.AppOp, uint64, error)
	settingsApplied uint64
	opsApplied      uint64

	// The promoter's hysteresis state, all guarded by sessionsMu and keyed
	// like standby/recvStamp. See tunnel_promoter.go.
	//
	// promoteSince is when a standby FIRST beat the worst idle active tunnel
	// by tunnelPromoteMargin, and is cleared the moment it stops doing so, so
	// only an advantage that HOLDS for tunnelPromoteHold moves a stream.
	// parkedAt is when a tunnel was parked, so tunnelParkMinHold can keep it
	// out for a while — the three dampers that ended the leg-level flap of
	// #4968 (14 parks in 65 s).
	promoteSince map[*yamux.Session]time.Time
	parkedAt     map[*yamux.Session]time.Time
	// tickBytes is each tunnel's cumulative rx+tx as of the PREVIOUS promoter
	// tick, so the tick can tell how many bytes the tunnel moved since — the
	// mid-transfer test. yamux stream counts alone are not that test: a range
	// split opens and closes a stream per chunk, so between two chunks a
	// tunnel carrying the whole object reads as idle for exactly as long as it
	// takes to open the next one, and a promoter tick landing in that gap
	// parks the tunnel that is delivering. Bytes on the wire have no such gap.
	tickBytes map[*yamux.Session]uint64

	// The idle capacity audition. A standby tunnel has an RTT but never a
	// capacity — tunnelMeter.sample only learns from a window in which the
	// tunnel carried streams (#4965) — so the promoter's own statistic can
	// never be checked against throughput. auditions holds every standby
	// tunnel currently allowed to take the next SIBLING stream, each mapped to
	// the instant its offer expires; auditionedAt rate-limits how often one
	// tunnel is offered. No extra bytes are moved: a stream that was going to
	// be carried anyway is carried by a different tunnel.
	//
	// Up to tunnel.audition_parallel offers may stand at once. One at a time
	// measured the pool at one tunnel per tunnel.audition_every (60 s), so a
	// pool of eight took eight minutes of idleness to measure — longer than
	// the gap between transfers ever is, which is how the spread policy kept
	// finding unmeasured routes to promote. The rails are unchanged: offers
	// are armed only while NOTHING is busy, taken only by a sibling stream,
	// one stream per offer, one offer per tunnel per tunnel.audition_every.
	auditions    map[*yamux.Session]time.Time
	auditionedAt map[*yamux.Session]time.Time

	// appProxyStatus PULLS the visor's rich per-tunnel snapshot, which is where
	// a tunnel's capacity PRIOR comes from (the per-hop transport throughput
	// the app cannot see for itself). Indirected like appSettings so a test can
	// drive it without an app RPC, and nil wherever there is no visor to ask.
	// priorsAt is when the last pull ran; it is touched only by the keepalive
	// loop, like settingsApplied.
	appProxyStatus func() (proxystatus.Snapshot, error)
	priorsAt       time.Time
}

// tunnelNote is one queued mux event on its way to the visor.
type tunnelNote struct {
	port   routing.Port
	event  string
	reason string
	role   string
}

// Tunnel roles, as reported to the visor at dial time (router.DialOptions
// TunnelRole) and shown by `visor state --select mux_route_groups`, `proxy mux
// info` and the proxy status page. They are the DIALING end's own labels: an
// exit sees a peer's tunnels but has no idea which of them are in standby.
const (
	// TunnelRoleActive is a tunnel the picker may put streams on.
	TunnelRoleActive = "active"
	// TunnelRoleStandby is a tunnel held open, kept alive and measured, but
	// carrying no streams while an active tunnel is live.
	TunnelRoleStandby = "standby"
)

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

// Unwrap returns the metered conn, so a caller that needs the yamux stream
// itself — to find the tunnel under it (sessionOf) — can see past the meter.
func (c *countingConn) Unwrap() net.Conn { return c.Conn }

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
// read AND write. The keepalive loop reads the stamp as proof the tunnel is
// still moving bytes, so it is never retired mid-transfer just because its
// ping/pong is queued behind the transfer's own frames (all ride the same
// conn). Reads cover a download (the remote is sending); writes cover an
// UPLOAD — there the tunnel receives nothing and the ping cannot get through
// the saturated send queue, so send progress is the only liveness evidence
// (measured live: a healthy 20MB upload retired at the hard-dead window ~112s
// in). A write that merely lands in a dead conn's buffers stamps briefly, but
// those buffers fill within seconds under load and the stamps stop — the
// hard-dead window still ages out a genuinely dead tunnel.
type recvStampConn struct {
	net.Conn
	m *tunnelMeter
}

func (c *recvStampConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.m.stamp.Store(time.Now().UnixNano())
		c.m.rx.Add(uint64(n)) //nolint:gosec // n > 0
	}
	return n, err
}

func (c *recvStampConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.m.stamp.Store(time.Now().UnixNano())
		c.m.tx.Add(uint64(n)) //nolint:gosec // n > 0
	}
	return n, err
}

// tunnelMeter is one tunnel's byte meter: the receive-time stamp above, the
// cumulative bytes each way, and the CAPACITY the tunnel has shown per
// direction — the peak rate observed while it carried streams, decaying only
// while it is busy so an idle tunnel keeps what it proved. pickSession weighs
// a new stream by it: on a rig where the direct tunnel uploads at 9 MB/s and
// the two-hop one at 5, the fewest-streams rule alone sent every upload to
// whichever was idle first (measured: 10 MB uploads at 1.6 MB/s on the slow
// tunnel against 6.5 on the fast one).
//
// Only a window in which the tunnel carried streams proves anything. An idle
// window moves keepalives and handshakes — tens of bytes — and a rate made of
// those is noise, yet it is nonzero, so the first version of this meter
// recorded it as the tunnel's capacity. Against a primary tunnel whose warm-up
// probes had "proven" a kilobyte per second, a tunnel proven at 23 B/s by one
// ping lost every pick, never carried a stream, and so was never re-measured:
// two- and three-tunnel range-split downloads rode one tunnel (measured: 0 of
// 10 MB on the second tunnel, 5 trials of 5). busyAt lets pickSession tell a
// fresh estimate from a stale one, so an idle tunnel is probed again.
type tunnelMeter struct {
	stamp atomic.Int64
	rx    atomic.Uint64
	tx    atomic.Uint64

	// port is the LOCAL port this tunnel was dialed from — the route group's
	// source port, and the only name the app and the visor share for one
	// tunnel. Set once at newYamuxSession from the dialed conn's local
	// address and never written again, so it needs no lock. 0 when the conn
	// is not an app conn (every unit test, and the single-tunnel fallback
	// paths), which simply means no event can be reported for it.
	port routing.Port

	// openTimeouts counts exit-open timeouts charged to this tunnel and
	// penaltyUntil (UnixNano; 0 = none) is the instant it may be picked again.
	// See exitOpenPenalty.
	openTimeouts atomic.Uint64
	penaltyUntil atomic.Int64

	// The snub half of the meter (tunnel_snub.go): how much work the tunnel
	// holds, when it last produced a byte or an ack, when its current run of
	// work began, and the hold it is serving. All atomics, because the snub
	// evaluator reads them from the keepalive loop while chunk goroutines
	// write them.
	outstanding atomic.Int64
	lastByteAt  atomic.Int64
	lastAckAt   atomic.Int64
	workAt      atomic.Int64
	snubUntil   atomic.Int64
	probing     atomic.Bool

	mu       sync.Mutex
	lastAt   time.Time
	lastRx   uint64
	lastTx   uint64
	busyAt   time.Time // when a busy window last updated the estimates
	rxCapBps float64
	txCapBps float64
	// rttMs is the tunnel's round-trip time in milliseconds, an EWMA of the
	// yamux pings the keepalive loop already issues (Session.Ping returns the
	// RTT). 0 = never measured. It is what a LONE stream is picked on — see
	// pickSessionFor.
	rttMs float64
	// rttWin holds the same pings raw, for the sliding MINIMUM the promoter
	// judges a swap on. The EWMA is the right statistic for placing a stream
	// on a tunnel right now; it is the wrong one for deciding that a tunnel is
	// permanently better, because a yamux ping shares the tunnel's send queue
	// with bulk data and so reads base RTT + this transfer's own queuing
	// delay. Queuing only ever ADDS, so the smallest sample in the recent past
	// is the estimate load cannot inflate — the same escape the leg-level band
	// took in legRTTWindow after one leg's samples walked 37 → 955 ms inside a
	// single download and flapped a park every 30 s.
	rttWin tunnelRTTWindow
	// gpBps is the tunnel's DELIVERED GOODPUT: an EWMA of the bytes it moved
	// per second, both directions together, over the windows in which it
	// actually carried streams. gpAt is when that estimate last moved and
	// gpWins counts the carrying windows folded into it.
	//
	// It is deliberately NOT rxCapBps/txCapBps. Those are PEAKS — the best
	// second a tunnel ever had, decayed — which is the right input for sizing
	// the next chunk and the wrong one for deciding that a tunnel should keep
	// its place in the active set: a tunnel that peaked at 9 MB/s once and has
	// delivered 300 kB/s ever since still reads 9 by peak and 300 by this. And
	// unlike the peak, a carrying window that delivered NOTHING is folded in
	// at its true value of zero, so a tunnel that stops delivering under load
	// says so here within a few windows instead of coasting on its best
	// moment.
	//
	// Bytes, not RTT, are what the promoter is finally allowed to park a
	// tunnel on. On the rig 2026-09-18 (bench/2026-09-18/9f4848dfa-smoke) the
	// promoter parked an active tunnel because a standby pinged 2.92x faster
	// (132 ms against 384 ms) — and the parked tunnel was the one carrying the
	// object. RTT is a property of the path's length; goodput is a property of
	// what the path delivers, and the two are not the same number on a mesh
	// whose nearest route is not its fattest.
	gpBps  float64
	gpAt   time.Time
	gpWins int
	// priorBps is the tunnel's capacity PRIOR: what its route can be expected
	// to carry from the transports it is built out of, before it has carried
	// anything itself. It is the min over the tunnel's hops of each hop
	// transport's observed peak goodput, taken over the tunnel's best leg
	// (tunnelPriorBps), and it arrives from the visor over the ProxyStatus RPC
	// — the app cannot see a transport, only the visor can.
	//
	// It is NOT a measurement and never becomes one: the moment a busy window
	// gives the tunnel a real rxCapBps/txCapBps, that is what every caller
	// reads and the prior is only the fallback again. Its whole job is to stop
	// an unmeasured standby from being credited the best capacity present —
	// the defect that let the spread policy promote a route that had never
	// carried a byte and then weigh it like the fastest one.
	priorBps float64
	// snubC is closed while the tunnel is snubbed and replaced on un-snub, so a
	// chunk in flight on the tunnel can select on the snub the way it selects
	// on the session dying. Guarded by mu like the rest of this block.
	snubC chan struct{}
}

// tunnelRTTAlpha weights each new ping into the tunnel's RTT EWMA. The first
// sample seeds it whole: an EWMA from zero would halve it, and a tunnel is
// judged by this number from its very first pick.
const tunnelRTTAlpha = 0.25

// recordRTT folds one yamux ping round-trip into the tunnel's RTT estimate.
func (m *tunnelMeter) recordRTT(d time.Duration) {
	if d <= 0 {
		return
	}
	ms := float64(d) / float64(time.Millisecond)
	m.mu.Lock()
	if m.rttMs == 0 {
		m.rttMs = ms
	} else {
		m.rttMs += setTunnelRTTAlpha() * (ms - m.rttMs)
	}
	m.rttWin.push(ms, time.Now())
	m.mu.Unlock()
}

// minRTT returns the smallest ping still inside the promoter's window, and
// whether the window holds one. This — not the EWMA — is what a SWAP is judged
// on; see tunnelRTTWindow.
func (m *tunnelMeter) minRTT(now time.Time) (ms float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms = m.rttWin.minMs(now)
	return ms, ms > 0
}

// rtt returns the tunnel's measured round-trip time in milliseconds; ok is
// false when it has never been pinged.
func (m *tunnelMeter) rtt() (ms float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rttMs, m.rttMs > 0
}

// meterSampleMin is the shortest interval a capacity sample is taken over;
// meterCapDecay is applied per sample while the tunnel is busy, so a capacity
// that a tunnel stops delivering is forgotten within a few seconds of load.
// meterFresh is how long a busy window's estimate stays authoritative for an
// idle tunnel; past it the tunnel is credited the best known capacity and
// probed like an unproven one, so no tunnel is starved by an old estimate.
const (
	meterSampleMin = 500 * time.Millisecond
	meterCapDecay  = 0.9
	meterFresh     = 2 * time.Second
)

// sample folds the bytes moved since the previous sample into the capacity
// estimates. busy says whether the tunnel carried streams over the interval:
// only a busy window updates the estimates (peak kept, decayed per sample), so
// idleness neither erodes a proven capacity nor invents one from keepalives.
//
// A DIRECTION is decayed only when that direction moved bytes. Decaying both
// per busy window let one direction's load erase the other's measurement:
// measured live 2026-09-17, after 34 s of downloads a tunnel's upload estimate
// had decayed 0.9^68 — effectively to nothing — so the next five 50 MB uploads
// were decided by download rates alone and every one of them went to the
// 2.6 MB/s tunnel instead of the 9.8 MB/s one. A window that asked nothing of a
// direction proves nothing about it. A busy window in which NEITHER direction
// moved is a stalled tunnel, and both estimates decay then, so the "a capacity
// a tunnel stops delivering is forgotten under load" rule survives intact.
func (m *tunnelMeter) sample(now time.Time, busy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rx, tx := m.rx.Load(), m.tx.Load()
	if m.lastAt.IsZero() {
		m.lastAt, m.lastRx, m.lastTx = now, rx, tx
		return
	}
	dt := now.Sub(m.lastAt)
	if dt < setMeterSampleMin() {
		return
	}
	secs := dt.Seconds()
	rxRate := float64(rx-m.lastRx) / secs
	txRate := float64(tx-m.lastTx) / secs
	m.lastAt, m.lastRx, m.lastTx = now, rx, tx
	if !busy {
		return
	}
	m.busyAt = now
	// The carrying window is also one goodput sample, folded in whole the
	// first time and by the alpha afterwards. A window that delivered nothing
	// counts as the zero it was: this is what the tunnel DELIVERED while it
	// was asked to carry, not the best it has ever managed.
	if rate := rxRate + txRate; m.gpWins == 0 {
		m.gpBps, m.gpWins, m.gpAt = rate, 1, now
	} else {
		m.gpBps += setTunnelGoodputAlpha() * (rate - m.gpBps)
		m.gpWins++
		m.gpAt = now
	}
	stalled := rxRate <= 0 && txRate <= 0
	if rxRate > 0 || stalled {
		m.rxCapBps *= setMeterCapDecay()
	}
	if txRate > 0 || stalled {
		m.txCapBps *= setMeterCapDecay()
	}
	if rxRate > m.rxCapBps {
		m.rxCapBps = rxRate
	}
	if txRate > m.txCapBps {
		m.txCapBps = txRate
	}
}

// exitOpenPenalty is how long a tunnel sits out the picks after an exit open
// timed out on it. A tunnel whose exit stops answering the SOCKS5 greeting
// delivers the browser nothing, yet it stays "live": yamux sees no error, so
// IsClosed() stays false, and the stale-idle rule above credits an idle tunnel
// the best known capacity — which steers the very next stream straight back
// onto the tunnel that just timed out (measured live during a ~55s all-paths
// blackout: two consecutive 10MB downloads through a 3-tunnel client returned
// nothing, each after exactly 15.0s). The window is short on purpose: long
// enough that the next stream tries a different tunnel, short enough that a
// tunnel recovering from a transient blackout is retried almost at once. It is
// cleared by the first successful exit open on the tunnel, and never applied to
// the only live tunnel — sitting out is only useful when something else can
// take the stream.
const exitOpenPenalty = 10 * time.Second

// bench charges an exit-open timeout to the tunnel and benches it for
// exitOpenPenalty.
func (m *tunnelMeter) bench(now time.Time) {
	m.openTimeouts.Add(1)
	m.penaltyUntil.Store(now.Add(setExitOpenPenalty()).UnixNano())
}

// unbench returns the tunnel to the picks; called when an exit open on it
// succeeds.
func (m *tunnelMeter) unbench() { m.penaltyUntil.Store(0) }

// onBench reports whether the tunnel is still benched at now.
func (m *tunnelMeter) onBench(now time.Time) bool {
	until := m.penaltyUntil.Load()
	return until > 0 && now.UnixNano() < until
}

// capacity returns the tunnel's proven DOWNLOAD capacity in bytes/s — what a
// range chunk will use. 0 means nothing proven yet. fresh says whether a busy
// window updated the estimate within meterFresh of now.
func (m *tunnelMeter) capacity(now time.Time) (bps float64, fresh bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fresh = !m.busyAt.IsZero() && now.Sub(m.busyAt) <= setMeterFresh()
	return m.rxCapBps, fresh
}

// goodput returns the tunnel's delivered goodput in bytes/s and whether that
// number is a MEASUREMENT rather than an absence of one.
//
// ok requires two things: at least tunnel.goodput_min_windows carrying windows
// (one window can be a sliver of a transfer, and a single sliver is how the
// capacity meter once "proved" 23 B/s from a keepalive — #4965), and a
// measurement no older than tunnel.goodput_fresh. Past that the tunnel is
// unmeasured again and the audition is what re-measures it.
//
// The VALUE may legitimately be zero while ok is true: a tunnel that carried
// streams and delivered nothing is measured at nothing, and that is exactly
// the tunnel the promoter should be allowed to replace.
func (m *tunnelMeter) goodput(now time.Time) (bps float64, ok bool) {
	if m == nil {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gpAt.IsZero() || m.gpWins < setTunnelGoodputMinWindows() {
		return 0, false
	}
	if now.Sub(m.gpAt) > setTunnelGoodputFresh() {
		return m.gpBps, false
	}
	return m.gpBps, true
}

// goodputAt is when the tunnel's delivered-goodput estimate last moved; a zero
// time means it never has. It is the age the parallel audition rotates on:
// with several offers standing at once, the standby whose last measurement is
// oldest is the one whose number is least worth trusting.
func (m *tunnelMeter) goodputAt() time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gpAt
}

// capacityTx returns the tunnel's proven UPLOAD capacity in bytes/s — what a
// striped-upload chunk will use. 0 means nothing proven yet. fresh says whether
// a busy window updated the estimate within meterFresh of now.
func (m *tunnelMeter) capacityTx(now time.Time) (bps float64, fresh bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fresh = !m.busyAt.IsZero() && now.Sub(m.busyAt) <= setMeterFresh()
	return m.txCapBps, fresh
}

// capacityDir returns the tunnel's proven capacity in the direction a chunk
// will use — upload for a striped PUT, download for a range GET — so the
// bandwidth-delay depth is computed from the direction it is about to size.
func (m *tunnelMeter) capacityDir(now time.Time, up bool) (bps float64, fresh bool) {
	if up {
		return m.capacityTx(now)
	}
	return m.capacity(now)
}

// setPrior installs the tunnel's capacity prior (bytes/s; <= 0 clears it).
func (m *tunnelMeter) setPrior(bps float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if bps < 0 {
		bps = 0
	}
	m.priorBps = bps
	m.mu.Unlock()
}

// prior returns the tunnel's capacity prior, 0 when it has none.
func (m *tunnelMeter) prior() float64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.priorBps
}

// capacityOrPrior is what a planner and the promoter read: the tunnel's proven
// capacity in dir when a busy window has produced one, and its PRIOR when none
// has.
//
// The three returns are the whole distinction the spread planner needs:
//
//   - bps > 0, prior == false: a measurement. fresh says whether a busy window
//     updated it within tunnel.meter_fresh.
//   - bps > 0, prior == true (fresh is then always false): no busy window has
//     ever sampled this tunnel, and this is what its transports say it should
//     manage. Weigh a share by it — but never mistake it for evidence.
//   - bps == 0: nothing measured and no prior. This is the route the planner
//     owes exactly one probe chunk and nothing more.
func (m *tunnelMeter) capacityOrPrior(now time.Time, up bool) (bps float64, fresh, prior bool) {
	if m == nil {
		return 0, false, false
	}
	bps, fresh = m.capacityDir(now, up)
	if bps > 0 {
		return bps, fresh, false
	}
	if p := m.prior(); p > 0 {
		return p, false, true
	}
	return 0, false, false
}

// pickDir is what a new stream will mostly do, for pickSessionFor.
//
// Only a range chunk (pickRecv) is capacity-weighted: it is one of several
// parallel streams whose whole point is to fill the pipe, so what matters is
// how much of the pipe each tunnel has been shown to deliver. A LONE stream —
// a browser connection, an upload — is pickAny, and capacity is the wrong
// statistic for it: it is not competing with anything, so it wants the tunnel
// that answers fastest, which is the direct or lowest-latency one.
//
// pickAny cannot be refined into an upload/download hint, either. The tunnel
// is picked at Accept and the exit stream it opens is what carries the SOCKS5
// CONNECT whose reply must arrive before the browser sends a byte, so the
// request head — the POST/PUT that would say "upload" — is peeked
// (rangeSplitInner, peekRequestHead) only AFTER the pick has been made.
type pickDir int

const (
	pickAny  pickDir = iota // a lone stream (browser connection, upload): lowest RTT wins
	pickRecv                // a range chunk: the tunnel will mostly deliver
	pickSend                // a striped-upload chunk: the tunnel will mostly send
)

// up reports whether the pick weighs the UPLOAD direction. A tunnel's two
// capacities are measured separately (tunnelMeter.sample decays a direction
// only when that direction moved bytes), and on the rig they differ by a factor
// of four on the same tunnel, so a striped-upload chunk scored on the download
// estimate is scored on a number that has nothing to do with what it will do.
func (d pickDir) up() bool { return d == pickSend }

// pickKind says whether the new stream is ONE OF SEVERAL parallel streams for
// the same transfer, or the only stream that transfer has. It decides nothing
// about weighting — that is pickDir — and only one thing: whether the stream
// may take a standby tunnel's audition offer (tunnel_promoter.go).
//
// A LONE stream may not. The entry stream is the worst possible audition: it
// is what the browser's connection is answered on, and for a splittable GET it
// is ALSO chunk0 — the 2 MiB probe whose body is copied straight to the browser
// ahead of every other chunk (rangesplit.go step 7), so a cold standby under it
// holds up the whole download rather than one eighth of it. Measured on the rig
// 2026-09-16 (bench/2026-09-16/03ece1e95-smoke, mux-tunnels-2 rows 10 and 13,
// 50 MB downloads): the two rows whose 2 MiB probe landed on a standby ran at
// 5.1 and 5.3 MB/s against 8.3-8.4 for the rows either side of them, with no
// retire, snub, promote or evict event on either — placement, not loss. A
// sequential rescue tail, an upload probe, an upload's completion fetch and a
// replayed POST body are lone in exactly the same way.
//
// A SIBLING stream may. It is one of N chunk streams the splitter (or the
// upload stripe) is spreading over the tunnels right now, so the bytes were
// going to be carried by some tunnel either way, nothing is holding the
// browser's next byte on its own, and a chunk whose tunnel disappoints is
// refetched on another one for free.
type pickKind int

const (
	pickLone    pickKind = iota // the entry stream, a rescue tail, an upload probe/replay
	pickSibling                 // one of several parallel chunk streams
)

// newYamuxSession wraps a dialed route-group conn in a yamux client session with
// skysocks's flow-control window, metering the tunnel's bytes for the keepalive
// loop's receive stamp and pickSession's capacity estimate. Shared by NewClient
// and AddTunnel so every tunnel is configured identically.
func newYamuxSession(conn net.Conn) (*yamux.Session, *tunnelMeter, error) {
	m := new(tunnelMeter)
	m.port = tunnelLocalPort(conn)
	sessionCfg := yamux.DefaultConfig()
	sessionCfg.EnableKeepAlive = false
	sessionCfg.MaxStreamWindowSize = muxStreamWindowBytes
	sessionCfg.ConnectionWriteTimeout = muxConnWriteTimeout
	session, err := yamux.Client(&recvStampConn{Conn: conn, m: m}, sessionCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating client: yamux: %w", err)
	}
	return session, m, nil
}

// tunnelLocalPort reads the local port a tunnel's conn was dialed from. It is
// the route group's SOURCE port, which is what names the tunnel to the visor
// (Client.noteTunnel -> app.Client.NoteMuxEvent -> router.NoteTunnelEvent).
// 0 for anything that is not an app conn — a test pipe, or a tunnel dialed
// over a network that does not carry an app address — and 0 simply means this
// tunnel's switches go unreported rather than reported wrongly.
func tunnelLocalPort(conn net.Conn) routing.Port {
	if conn == nil {
		return 0
	}
	if a, ok := conn.LocalAddr().(appnet.Addr); ok {
		return a.Port
	}
	return 0
}

// NewClient constructs a new single-tunnel Client. Signature unchanged: this is
// the common case and every existing caller/test builds one tunnel this way. Use
// AddTunnel (or NewMultiClient) to stripe browser connections across N tunnels.
func NewClient(conn net.Conn, appCl *app.Client) (*Client, error) {
	c := &Client{
		appCl:         appCl,
		closeC:        make(chan struct{}),
		keepAliveDone: make(chan struct{}),
		streams:       make(map[uint32]streamMeta),
		// One tunnel is the default target: with N==1 the sole tunnel's death is
		// total collapse (handled by the app's --reconnect), so N==1 never
		// re-dials — byte-identical to the pre-aggregation build. NewMultiClient
		// raises this to the conn count, and the app sets --tunnels via
		// SetTunnelTarget so a re-dial can refill even after a short initial dial.
		target: 1,
		rs:     defaultRangeSplitConfig(),
		// Snapshotted before the keepalive goroutine below starts, so the loop
		// never reads the package-level vars.
		probeInterval:  livenessProbeInterval,
		hardDeadWindow: sessionHardDeadWindow,
		probeFailWin:   sessionProbeFailWindow,
	}

	session, stamp, err := newYamuxSession(conn)
	if err != nil {
		return nil, err
	}
	c.sessions = []*yamux.Session{session}
	c.recvStamp = map[*yamux.Session]*tunnelMeter{session: stamp}

	if appCl != nil {
		c.muxNote = appCl.NoteMuxEvent
		c.appSettings = appCl.AppSettings
		c.appProxyStatus = appCl.ProxyStatus
	}
	c.startMuxNotes()

	go func() {
		defer close(c.keepAliveDone)
		c.sessionKeepAliveLoop()
	}()

	return c, nil
}

// muxNoteBuffer is how many tunnel events may be queued for the visor at once.
// A failover writes two (retired + promoted) and the promoter at most two per
// 5 s tick, so this is ~2 minutes of the worst churn the dampers allow — far
// more than a visor answering an RPC in milliseconds can fall behind by.
const muxNoteBuffer = 64

// startMuxNotes runs the single goroutine that drains queued tunnel events to
// the visor, in order, off the keepalive loop's back. No-op when no app client
// wired a reporter.
func (c *Client) startMuxNotes() {
	if c.muxNote == nil {
		return
	}
	c.muxNoteC = make(chan tunnelNote, muxNoteBuffer)
	go func() {
		for {
			select {
			case <-c.closeC:
				return
			case n := <-c.muxNoteC:
				if err := c.muxNote(n.port, n.event, n.reason, n.role); err != nil && c.appCl != nil {
					c.appCl.Log().Debugf("Reporting %s for tunnel on port %d failed: %v", n.event, n.port, err)
				}
			}
		}
	}()
}

// noteTunnel queues one tunnel event for the visor: it lands on the router's
// mux-event ring (`visor state --select diag` .diag.mux_events, and the
// group's own events in `proxy mux events`) stamped with the route group's
// first hop, and role — when non-empty — re-labels that group's tunnel_role so
// `visor state --select mux_route_groups` says what the tunnel is NOW.
//
// Never blocks: a full queue drops the event rather than holding up the
// keepalive loop. Silent when the session has no local port (a test pipe) or
// no reporter is wired.
func (c *Client) noteTunnel(s *yamux.Session, event, reason, role string) {
	if s == nil {
		return
	}
	c.sessionsMu.Lock()
	m := c.recvStamp[s]
	c.sessionsMu.Unlock()
	if m == nil {
		return
	}
	c.queueTunnelNote(m.port, event, reason, role)
}

// queueTunnelNote is noteTunnel for a caller that already holds the tunnel's
// local port — the retire path, which drops the meter as it goes.
func (c *Client) queueTunnelNote(port routing.Port, event, reason, role string) {
	if c.muxNoteC == nil || port == 0 {
		return
	}
	select {
	case c.muxNoteC <- tunnelNote{port: port, event: event, reason: reason, role: role}:
	default:
	}
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
	return c.addTunnel(conn, false)
}

// ErrClientClosed is returned by AddTunnel / AddStandbyTunnel when the client
// has already been closed. The caller owns the conn it was about to hand over
// and must close it.
var ErrClientClosed = errors.New("skysocks: client is closed")

// addTunnel is the body of AddTunnel / AddStandbyTunnel: it wraps conn in a
// yamux session and registers its meter and its standby mark together, so a
// pool tunnel is never briefly visible to the picker as an active one.
//
// A dial that lands after Close is refused. The pool-fill and re-dial dials run
// in goroutines that nothing joins, so one of them can complete after Close has
// taken its snapshot of the tunnel set and closed it — and the fresh route
// group appended behind that snapshot would then be closed by nothing at all
// (the keepalive loop has returned and --reconnect builds a whole new Client),
// leaving it registered on the exit and the setup node until its rules expire.
// Reconnect is exactly when a dial is in flight. The closeC check sits INSIDE
// the sessionsMu hold because Close closes closeC before it takes that lock:
// either this append happens first and Close's snapshot covers it, or Close
// snapshots first and this call sees the closed channel.
func (c *Client) addTunnel(conn net.Conn, standby bool) error {
	session, stamp, err := newYamuxSession(conn)
	if err != nil {
		return err
	}
	c.sessionsMu.Lock()
	select {
	case <-c.closeC:
		c.sessionsMu.Unlock()
		_ = session.Close() //nolint:errcheck,gosec
		return ErrClientClosed
	default:
	}
	c.sessions = append(c.sessions, session)
	if c.recvStamp == nil {
		c.recvStamp = make(map[*yamux.Session]*tunnelMeter)
	}
	c.recvStamp[session] = stamp
	if standby {
		if c.standby == nil {
			c.standby = make(map[*yamux.Session]bool)
		}
		c.standby[session] = true
	}
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

// SetRangeSplitPort sets the destination port the splitter treats as plaintext
// HTTP (default 80). Non-positive values keep the default.
func (c *Client) SetRangeSplitPort(port int) {
	if port > 0 && port <= 65535 {
		c.rs.plainPort = port
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

// Bounded retry after a pool dial FAILS — as opposed to being refused for want
// of a disjoint first hop, which is a settled fact about the topology and is
// never retried.
//
// A failure says nothing about how many disjoint routes exist. Measured on the
// rig 2026-09-17: the pool filled to its ceiling of eight, three of the eight
// tunnels then died on unreachable peers, each death re-armed the fill, the
// three replacement dials failed, and the fill parked itself permanently at
// five — with four rig intermediates still free. Treating a failure like
// exhaustion is what kept it from the real bound.
//
// So a failure buys a second look, and a bounded one: maxRedialFails dials make
// a round, each round waits longer than the last, and after poolRetryRounds the
// fill rests until a tunnel death re-arms it. Worst case is 3x3 = 9 dials
// spread over ~3.5 minutes, one ever in flight — three orders of magnitude off
// the #4325 storm, and it still terminates on its own.
const (
	poolRetryBackoffBase = 30 * time.Second
	poolRetryBackoffMax  = 4 * time.Minute
	poolRetryRounds      = 3
)

// poolRetryDelay is the wait before failure round n (1-based): 30s, 60s, 120s,
// capped at poolRetryBackoffMax.
func poolRetryDelay(round int) time.Duration {
	ceil := setPoolRetryBackoffMax()
	d := setPoolRetryBackoffBase()
	for i := 1; i < round && d < ceil; i++ {
		d *= 2
	}
	if d > ceil {
		d = ceil
	}
	return d
}

// SetPoolDial wires the app's REQUIRE-disjoint dial into the Client so the
// standby pool can grow itself. It mirrors SetTunnelRedial — the dial lives in
// the app (cmd/apps/skysocks-client) because it needs the server PK, the
// retrier and the appnet fallback machinery — but differs in one decisive way:
// this dial demands a first hop no tunnel already held occupies, and returns
// router.ErrNoDisjointFirstHop rather than conceding a shared one. A shared
// extra tunnel aggregates nothing, and the refusal is the signal the pool
// stops on. A nil fn (the default) leaves the pool disabled.
func (c *Client) SetPoolDial(fn func() (net.Conn, error)) {
	c.redialMu.Lock()
	c.poolDial = fn
	c.redialMu.Unlock()
}

// SetStandbyPool sets the CEILING on tunnels held to the exit, the active ones
// included, and arms the fill. n <= the active target means "active tunnels
// only" and leaves the pool off — so --standby-pool 0 is byte-identical to the
// behavior before the pool existed.
//
// It is a ceiling and not a target: the fill stops for good at the first
// router.ErrNoDisjointFirstHop, so an exit with three disjoint first hops
// settles at three and never dials again until a tunnel dies. Setting it does
// not reap anything — an already-held tunnel is never given up, because the
// active target alone decides which tunnels carry streams.
//
// Which of the two actually stops a given fill depends on how many transports
// the visor holds, since a first hop is one of its own transports. A visor with
// a handful settles on exhaustion; one on the public network settles on this
// ceiling with plenty left — the rig's eighth pool dial had 124 free ranked
// candidates. StandbyPoolState's reason says which happened.
func (c *Client) SetStandbyPool(n int) {
	if n < 0 {
		n = 0
	}
	c.redialMu.Lock()
	c.poolMax = n
	if n > 0 {
		c.poolArmed = true
		c.poolFails = 0
		c.poolRetryRound = 0
		c.poolRetryAt = time.Time{}
	}
	c.redialMu.Unlock()
}

// StandbyPoolState reports the pool's size (tunnels currently held, active and
// standby), whether the fill has settled, when, and WHY.
//
// The reason is the part a bench needs, because "settled" alone conflates two
// very different outcomes: "pool ceiling" means --standby-pool is what stopped
// the fill and the topology had more to give, while "no disjoint first hop
// left" means the fill really did reach the topology's bound. On a visor
// holding a hundred-odd transports the first is the ordinary case — measured on
// the rig 2026-09-17, the eighth pool dial still had 124 free ranked candidates
// to choose from.
func (c *Client) StandbyPoolState() (held, standby int, settled bool, settledAt time.Time, reason string) {
	c.sessionsMu.Lock()
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() {
			continue
		}
		held++
		if c.standby[s] {
			standby++
		}
	}
	c.sessionsMu.Unlock()
	c.redialMu.Lock()
	settledAt = c.poolSettledAt
	reason = c.poolSettledReason
	c.redialMu.Unlock()
	return held, standby, !settledAt.IsZero(), settledAt, reason
}

// IsStandby reports whether s is held in the pool rather than carrying streams.
func (c *Client) IsStandby(s *yamux.Session) bool {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	return c.standby[s]
}

// AddStandbyTunnel is AddTunnel for a POOL tunnel: the session joins the set
// but is marked standby, so the picker passes it over while any active tunnel
// is live. The keepalive loop treats it like any other tunnel — same 5 s RTT
// ping, same hard-dead window — which is the whole point: its measurement is
// current the moment it is needed.
func (c *Client) AddStandbyTunnel(conn net.Conn) error {
	return c.addTunnel(conn, true)
}

// activeLiveCount counts the live tunnels that are NOT in standby — the width
// the picker actually stripes streams across.
func (c *Client) activeLiveCount() int {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	n := 0
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || c.standby[s] {
			continue
		}
		n++
	}
	return n
}

// standbyRTTStale is how long a standby tunnel's last sign of life may be
// before its RTT is treated as unproven for RANKING. The keepalive loop pings
// every tunnel every tunnelRTTProbeInterval, so three intervals of silence
// means the tunnel is not answering — exactly the "silently black-holing
// standby promoted into a transfer" case. It never makes a tunnel ineligible
// for FAILOVER, only worse-ranked: with an active tunnel already dead, a
// standby of unknown latency still beats an 8-9 s route setup.
const standbyRTTStale = 3 * tunnelRTTProbeInterval

// promoteBestStandby moves the best live standby tunnel into the active set
// and returns it (nil when the pool holds nothing usable).
//
// "Best" is the lowest yamux-ping RTT, the one statistic a tunnel carrying
// nothing actually has. It is a true symmetric end-to-end round trip, unlike
// the router's per-leg route_latency_ms, whose pong always replies on leg 0.
// A tunnel that has gone quiet for standbyRTTStale sorts behind every fresh
// one but is still eligible, and a benched tunnel (its exit open timed out)
// behind that again: at a failover the pool is what there is. For the same
// reason a park hold does not apply here — tunnelParkMinHold exists to stop
// the promoter trading two tunnels back and forth, not to withhold the last
// live route from a client that just lost its active one.
//
// The flip is two map entries and one event. No stream is migrated, because a
// standby tunnel by definition carries none.
func (c *Client) promoteBestStandby(reason string) *yamux.Session {
	now := time.Now()
	c.sessionsMu.Lock()
	var (
		best  *yamux.Session
		bestK [3]float64 // benched, stale, rtt — lower wins, in that order
	)
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || !c.standby[s] {
			continue
		}
		m := c.recvStamp[s]
		k := [3]float64{0, 1, 0}
		if m != nil {
			if m.onBench(now) {
				k[0] = 1
			}
			if ms, ok := m.rtt(); ok {
				k[2] = ms
				if ns := m.stamp.Load(); ns > 0 && now.Sub(time.Unix(0, ns)) <= setStandbyRTTStale() {
					k[1] = 0
				}
			}
		}
		if best == nil || k[0] < bestK[0] ||
			(k[0] == bestK[0] && k[1] < bestK[1]) ||
			(k[0] == bestK[0] && k[1] == bestK[1] && k[2] < bestK[2]) {
			best, bestK = s, k
		}
	}
	if best != nil {
		delete(c.standby, best)
		delete(c.auditions, best)
	}
	c.sessionsMu.Unlock()
	if best == nil {
		return nil
	}
	return c.notePromoted(best, reason)
}

// notePromoted publishes a promotion whose standby-map flip has already been
// made under sessionsMu, so the two promoters below report it identically.
func (c *Client) notePromoted(s *yamux.Session, reason string) *yamux.Session {
	c.noteTunnel(s, router.MuxEventTunnelPromoted, reason, TunnelRoleActive)
	if c.appCl != nil {
		c.appCl.Log().Infof("Promoted a standby tunnel into the active set (%s); %d active of %d held",
			reason, c.activeLiveCount(), c.liveSessionCount())
	}
	return s
}

// promoteFastestStandby is promoteBestStandby for a SPREAD: the tunnel picked
// is the standby with the best CAPACITY in dir, not the lowest RTT.
//
// The distinction is the second half of the 5.13 vs 8.23 MB/s row. min_routes
// promoted through the RTT rank, and the fastest standby of that run — the one
// with a real capacity sample behind it — stayed in the pool while a nearer but
// slower route took the third slot; the planner then weighed the shares
// correctly over a set whose third member could not carry much. Capacity is the
// statistic the planner itself assigns on (spreadCandidates), so the set it is
// handed is now chosen on it too.
//
// The rank is three tiers, and the tiers matter more than the numbers inside
// them:
//
//  1. MEASURED. A standby with a busy-window capacity sample in dir, highest
//     first. Evidence beats everything.
//  2. PRIOR. No sample, but its hops' transports say what it should manage
//     (tunnelMeter.priorBps), highest first. A guess from real transport
//     throughput is worth more than a ping and less than a byte.
//  3. NEITHER. Nothing measured and no prior: last, ranked among themselves on
//     RTT. This is the tier the spread policy used to promote FIRST, by
//     crediting an unmeasured route the best capacity present.
//
// A benched standby (its exit open timed out) is no candidate at all. With
// every candidate in tier 3 this falls straight back to promoteBestStandby, so
// the FAILOVER ranking is untouched — it is still what runs when nothing is
// known, and it is still what the failover paths call directly.
func (c *Client) promoteFastestStandby(dir spreadDir, reason string) *yamux.Session {
	now := time.Now()
	c.sessionsMu.Lock()
	var (
		best     *yamux.Session
		bestTier = 3
		bestBps  float64
	)
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || !c.standby[s] {
			continue
		}
		m := c.recvStamp[s]
		if m == nil || m.onBench(now) {
			continue
		}
		bps, _, isPrior := m.capacityOrPrior(now, dir == spreadUp)
		if bps <= 0 {
			continue // tier 3: promoteBestStandby's RTT rank owns these
		}
		tier := 1
		if isPrior {
			tier = 2
		}
		if tier < bestTier || (tier == bestTier && bps > bestBps) {
			best, bestTier, bestBps = s, tier, bps
		}
	}
	if best != nil {
		delete(c.standby, best)
		delete(c.auditions, best)
	}
	c.sessionsMu.Unlock()
	if best == nil {
		return c.promoteBestStandby(reason)
	}
	return c.notePromoted(best, reason)
}

// parkTunnel returns an ACTIVE tunnel to the pool: it stops being picked, goes
// on being pinged and measured, and can be promoted again later. Reports
// whether it was active to begin with.
//
// Parking never touches streams. A tunnel carrying any is not a candidate —
// the caller checks that — because the pool's whole bargain is that switching
// costs nothing in flight.
func (c *Client) parkTunnel(s *yamux.Session, reason string) bool {
	if s == nil {
		return false
	}
	c.sessionsMu.Lock()
	if c.standby[s] {
		c.sessionsMu.Unlock()
		return false
	}
	if c.standby == nil {
		c.standby = make(map[*yamux.Session]bool)
	}
	c.standby[s] = true
	// Every park starts its own hold, so the promoter cannot take a tunnel
	// straight back (tunnelParkMinHold). A failover ignores the hold, by
	// design — see promoteBestStandby.
	if c.parkedAt == nil {
		c.parkedAt = make(map[*yamux.Session]time.Time)
	}
	c.parkedAt[s] = time.Now()
	c.sessionsMu.Unlock()
	c.noteTunnel(s, router.MuxEventTunnelParked, reason, TunnelRoleStandby)
	return true
}

// retireTunnel closes a tunnel the keepalive loop found dead and, when it was
// an ACTIVE one, replaces it from the standby pool IN THE SAME TICK — before
// any re-dial is considered.
//
// This is what the pool is for. Before it, the active width came back only
// from a fresh dial: up to sessionHardDeadWindow (45 s) to notice the death,
// then a route setup (8-9 s typical, dialSetupCeiling 90 s at worst) to
// replace it, and the measured ttfb after a first-hop cut was 35-40 s. A
// promote is a map lookup on a route group that is already set up, already
// noise-handshaked and already being pinged.
//
// What it does NOT fix: a lone stream already spliced onto the cut tunnel
// (a POST) still dies with it — handleStream has no retry and stream migration
// is out of scope. Range chunks were already rescued in one round trip by
// tunnelGuard. The promise here is that the NEXT stream lands instantly on a
// live tunnel and the client never collapses, not that the in-flight upload
// survives.
//
// The dead group is NOT rebuilt as active: maybeRedial sees a full active set
// and stands down, and the pool fill — re-armed by this very death, since the
// first hop it held is free again — dials the replacement into the pool TAIL
// in the background. So the surviving route groups are left exactly as they
// were.
// Retiring is once-only per tunnel: the meter's presence is the ledger — this
// drops it — so a second sighting (a keepalive tick iterating a snapshot taken
// before the retire) reports false and does nothing. A promote per tick would
// drain the pool over one death.
//
// The session pointer goes with it. Nothing else prunes c.sessions, and a dead
// yamux session left in the slice keeps its stream map and its recv buffers
// alive for the life of the client while pickSessionFor walks it — taking
// yamux's locks — on every stream open, one per range chunk. Every reader of
// c.sessions holds sessionsMu for the whole read and none of them keeps an
// index across the lock, so compacting here desynchronises nothing.
func (c *Client) retireTunnel(s *yamux.Session, reason string) bool {
	if s == nil {
		return false
	}
	c.sessionsMu.Lock()
	m := c.recvStamp[s]
	if m == nil {
		c.sessionsMu.Unlock()
		return false // already retired on an earlier tick
	}
	wasStandby := c.standby[s]
	port := m.port
	delete(c.recvStamp, s)
	delete(c.standby, s)
	for i, held := range c.sessions {
		if held == s {
			c.sessions = append(c.sessions[:i], c.sessions[i+1:]...)
			break
		}
	}
	c.sessionsMu.Unlock()

	_ = s.Close() //nolint:errcheck
	c.forgetTunnel(s)
	c.queueTunnelNote(port, router.MuxEventTunnelRetired, reason, "")
	// The death itself re-arms the fill and the re-dial backoff. The keepalive
	// loop's level check (live < prevLive across two 15 s ticks) is a backstop,
	// not the trigger: a death whose replacement lands inside the same window
	// leaves the level unchanged, and the pool — already settled at "pool
	// ceiling" — would then stay one tunnel short for good. This path observes
	// every death exactly once.
	//
	// The fill is armed one probe interval LATER, though, and that hold-off is
	// the point of routing it through here rather than dialing on the spot.
	// Measured on the rig 2026-09-17 (fe53f42dc): arming inline put the refill
	// dial 11 s after the group closed, while the first hop the cut tunnel had
	// occupied was not dialable again yet. The diversify search therefore had
	// all seven held first hops excluded and nothing left but a sudph leg — the
	// 0.4-0.5 MB/s route maybePoolFill's comment describes — and two 50 MB
	// uploads rode it. Healthy builds refilled at 21-22 s, one keepalive tick
	// after the death, which is about what the freed hop needs to come back.
	// Liveness never waits on this: the failover promote below is immediate and
	// this only schedules a dial that GROWS the pool.
	c.resetRedialBackoff()
	c.armPoolFillAfter(c.probeInterval)
	if !wasStandby {
		c.promoteBestStandby("failover: active tunnel died")
	}
	return true
}

// armPoolFill re-opens the fill after it has settled. Called on a FRESH tunnel
// death (and nowhere else): the set of tunnels we hold just changed, so the
// "no disjoint first hop left" answer the router gave is stale — the hop the
// dead tunnel occupied is free again. Everything else leaves the pool resting,
// which is what keeps this from being a dial loop.
func (c *Client) armPoolFill() { c.armPoolFillAfter(0) }

// armPoolFillAfter is armPoolFill with a hold-off: the fill is armed now, but
// maybePoolFill serves out delay before the first dial, through the same
// poolRetryAt gate a failure round already uses. A retire arms it one probe
// interval out so the first hop the dead tunnel held gets its restore window
// before the diversify search is asked which hops are still free; every other
// caller arms with delay 0. A second death inside the window re-arms with a
// fresh window, which is right — the newest freed hop is the one to wait for.
func (c *Client) armPoolFillAfter(delay time.Duration) {
	c.redialMu.Lock()
	if c.poolMax > 0 {
		c.poolArmed = true
		c.poolFails = 0
		c.poolRetryRound = 0
		c.poolRetryAt = time.Time{}
		if delay > 0 {
			c.poolRetryAt = time.Now().Add(delay)
		}
		c.poolSettledAt = time.Time{}
		c.poolSettledLogged = false
	}
	c.redialMu.Unlock()
}

// settlePool records where the fill came to rest and disarms it, logging the
// one line that says how wide the pool got and why it stopped. Idempotent: the
// line is printed once per settle, not once per tick.
func (c *Client) settlePool(held int, reason string) {
	c.redialMu.Lock()
	first := !c.poolSettledLogged
	c.poolArmed = false
	c.poolSettledAt = time.Now()
	c.poolSettledN = held
	c.poolSettledReason = reason
	c.poolSettledLogged = true
	c.redialMu.Unlock()
	if first && c.appCl != nil {
		c.appCl.Log().Infof("standby pool settled at %d tunnel(s) (%s)", held, reason)
	}
}

// notePoolDialFailure books one failed pool dial and decides what happens next.
//
// A failure is not exhaustion: it says the dial did not land, not that the
// topology has no disjoint route left. Within a round the next tick simply
// tries again — and usually onto a DIFFERENT first hop, since the router
// re-ranks and the intermediate that just failed is excluded from the retry.
// After maxRedialFails in a row the round closes and the fill waits out a
// backoff before the next one; after poolRetryRounds it rests until a tunnel
// death re-arms it.
func (c *Client) notePoolDialFailure(err error) {
	c.redialMu.Lock()
	c.poolFails++
	fails := c.poolFails
	var wait time.Duration
	round := c.poolRetryRound
	giveUp := false
	if fails >= maxRedialFails {
		c.poolFails = 0
		c.poolRetryRound++
		round = c.poolRetryRound
		if round >= setPoolRetryRounds() {
			giveUp = true
		} else {
			wait = poolRetryDelay(round)
			c.poolRetryAt = time.Now().Add(wait)
		}
	}
	c.redialMu.Unlock()

	if giveUp {
		c.settlePool(c.liveSessionCount(), "dial failures")
		return
	}
	if c.appCl == nil {
		return
	}
	if wait > 0 {
		c.appCl.Log().Warnf("Standby pool dial failed %d times; pausing the fill for %v (round %d/%d): %v",
			maxRedialFails, wait, round, setPoolRetryRounds(), err)
		return
	}
	c.appCl.Log().Warnf("Standby pool dial failed (%d/%d): %v", fails, maxRedialFails, err)
}

// maybePoolFill dials up to setup.fill_inflight more sibling tunnels toward the
// pool ceiling, concurrently.
//
// It is the tunnel-level twin of the mux's leg self-heal, and it obeys the
// same discipline, for the same reason. #4325 turned a pool TARGET of 513 into
// an endless dial loop that the whole fleet felt, because an unreachable
// target is chased forever. So this one is not a target at all:
//
//   - at most setup.fill_inflight dials in flight (poolFillInFlight), launched
//     TOGETHER so the setup dialer can coalesce them into one batched request;
//   - stop at the ceiling, and record it;
//   - stop for good at the first router.ErrNoDisjointFirstHop — that answer is
//     about the topology, not about luck, and re-asking cannot change it;
//   - give an ordinary dial FAILURE a bounded second look and no more —
//     poolRetryRounds rounds of maxRedialFails dials behind a growing backoff
//     (notePoolDialFailure), because a failure says nothing about how many
//     disjoint routes exist, but an unreachable exit must not be hammered;
//   - re-arm ONLY when a tunnel dies (armPoolFill), because that is the one
//     event that makes a first hop free again.
//
// Nothing here ever removes a tunnel. The pool is never reaped: the active
// target alone decides which tunnels carry streams.
func (c *Client) maybePoolFill() {
	// A lowered ceiling is answered on the same tick a raised one is (see
	// maybePoolShrink), and before the fill reads the ceiling itself.
	c.maybePoolShrink()
	if setPoolFreeze() {
		// pool.freeze: the set of tunnels held is the operator's to change, not
		// the fill's. A tunnel DEATH still re-arms the fill, so the freeze is
		// lifted into a refill the moment it is cleared rather than leaving the
		// pool permanently short.
		return
	}
	c.redialMu.Lock()
	fn := c.poolDial
	poolMax := c.poolMax
	armed := c.poolArmed
	retryAt := c.poolRetryAt
	target := c.target
	c.redialMu.Unlock()

	if fn == nil || poolMax <= 0 || !armed {
		return
	}
	// Serving out a failure round's backoff. The fill is still armed and will
	// resume on its own; this is the wait between rounds, not a rest.
	if !retryAt.IsZero() && time.Now().Before(retryAt) {
		return
	}
	held := c.liveSessionCount()
	if held <= 0 {
		// Total collapse is the app's --reconnect business, not the pool's.
		return
	}
	if held >= poolMax {
		c.settlePool(held, "pool ceiling")
		return
	}
	// How many dials to launch on this tick: up to the in-flight bound, never
	// more than the gap to the ceiling. Launching them TOGETHER is the point —
	// the setup dialer coalesces concurrent dials to one exit into a single
	// batched setup request, and one serial dial per tick gave it nothing to
	// coalesce (and built a pool of eight in ~56 s of setup-node round trips).
	inflight := router.SetupFillInflight()
	want := poolMax - held
	if want > inflight {
		want = inflight
	}
	launched := 0
	for i := 0; i < want; i++ {
		if int(c.poolFillInFlight.Add(1)) > inflight {
			c.poolFillInFlight.Add(-1)
			break
		}
		launched++
	}
	if launched == 0 {
		return // every slot is busy
	}
	for i := 0; i < launched; i++ {
		go c.onePoolDial(fn, target)
	}
}

// onePoolDial is one standby-pool dial: it grows the pool by a tunnel, or
// records why it could not. Several run concurrently, bounded by
// setup.fill_inflight; each holds one of those slots.
func (c *Client) onePoolDial(fn func() (net.Conn, error), target int) {
	// The slot is released however this dial ends, including the several early
	// returns below; the closure keeps that one defer next to the work.
	func() {
		defer c.poolFillInFlight.Add(-1)
		conn, err := fn()
		if err != nil {
			if app.IsNoDisjointFirstHop(err) {
				// The settled answer: every route to the exit now leaves over a
				// first hop one of our tunnels already holds. This IS the size
				// of the pool.
				c.settlePool(c.liveSessionCount(), "no disjoint first hop left")
				return
			}
			c.notePoolDialFailure(err)
			return
		}
		// A pool dial ALWAYS lands in standby, never in the active set.
		//
		// It used to join the active set whenever that set was short, and
		// measured on the rig 2026-09-17 (campaign21, develop 67dddb8be) that
		// was a bad trade: the bench cut the active Atlanta tunnel's first hop
		// with six healthy stcpr standbys held, the refill dial excluded every
		// held hop and took the next-ranked candidate — a SUDPH hop to a fleet
		// peer — and that brand-new group went straight into the active set.
		// Every later lone upload picked it (pickAny is lowest RTT, and a
		// fresh tunnel's first ping was good), so the three 50 MB uploads ran
		// at 0.45-0.52 MB/s against a 9-10 MB/s reference while six held
		// tunnels sat idle. A dial's RANK is not evidence that a route carries
		// traffic well; a held tunnel that has been pinged for minutes is at
		// least measured.
		//
		// So the two jobs are separated: the fill only ever grows the pool,
		// and promotion — from the pool, by measurement — is the only way into
		// the active set. If the active set is short when a pool tunnel lands
		// (the pool was empty when its predecessor died), the promote happens
		// immediately, but it goes through the same ranking as any other, so
		// the tunnel that takes the slot is the best HELD one, not merely the
		// newest.
		if aerr := c.addTunnel(conn, true); aerr != nil {
			_ = conn.Close() //nolint:errcheck,gosec
			c.notePoolDialFailure(aerr)
			return
		}
		// The pool just grew, so if the active set is still short — nothing was
		// held when its predecessor died — promote now, by rank. Usually a
		// no-op: the failover already refilled the set from the pool.
		if c.activeLiveCount() < target {
			c.promoteBestStandby("failover: active set short after a pool dial landed")
		}
		// Progress: the topology is answering, so the failure ledger starts over.
		c.redialMu.Lock()
		c.poolFails = 0
		c.poolRetryRound = 0
		c.poolRetryAt = time.Time{}
		c.redialMu.Unlock()
		if c.appCl != nil {
			held, standby, _, _, _ := c.StandbyPoolState()
			c.appCl.Log().Infof("Standby pool grew by one tunnel; %d held (%d standby)", held, standby)
		}
	}()
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
//
// The target is compared against the ACTIVE tunnel count, not the total held.
// Without a pool those are the same number and nothing changes. With one, a
// death is answered in the same tick by promoting a standby (retireTunnel), so
// by the time this runs the active set is already whole and the re-dial stands
// down — the replacement route is dialed into the pool TAIL by maybePoolFill
// instead, which is what leaves every surviving route group untouched. A
// re-dial happens only when the pool had nothing to give, and then an ACTIVE
// replacement is exactly the right answer.
func (c *Client) maybeRedial(live int) {
	c.redialMu.Lock()
	fn := c.redial
	target := c.target
	if target < 1 {
		target = 1
	}
	backedOff := c.redialFails >= maxRedialFails
	c.redialMu.Unlock()

	if fn == nil || backedOff || c.activeLiveCount() >= target || live <= 0 {
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
	m := c.recvStamp[s]
	c.sessionsMu.Unlock()
	if m == nil {
		return time.Time{}
	}
	if ns := m.stamp.Load(); ns > 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// recordTunnelRTT folds a yamux ping round-trip into the tunnel's RTT
// estimate, which is what pickSessionFor(pickAny) sorts on.
func (c *Client) recordTunnelRTT(s *yamux.Session, rtt time.Duration) {
	c.sessionsMu.Lock()
	m := c.recvStamp[s]
	c.sessionsMu.Unlock()
	if m != nil {
		m.recordRTT(rtt)
	}
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

// pickSession returns the tunnel for a LONE stream — an accepted browser
// connection, an upload — or nil when every tunnel is closed. With a single
// tunnel it always returns that tunnel while it is live (identical to the
// pre-aggregation c.session).
func (c *Client) pickSession() *yamux.Session {
	return c.pickSessionFor(pickAny)
}

// pickSessionFor picks by the stream's shape.
//
// A LONE stream (pickAny) goes to the live tunnel with the lowest measured
// RTT PER OPEN STREAM: rtt × (open streams + 1), lowest wins. It is not
// competing for the pipe with sibling streams, so the statistic that matters
// is how fast the tunnel answers, not how much it has been shown to move; a
// tunnel that never answered a ping sorts last (it is unproven, not fast), and
// ties go to the tunnel with the fewest open streams. Weighing a lone stream
// by capacity is what put all five of a measured 50 MB upload set on a 470 ms
// Sydney tunnel instead of the direct one (0.23 MB/s against 9.8).
//
// The (streams + 1) factor is what keeps concurrent lone streams apart. With
// every tunnel idle it cancels out and the rule is exactly lowest RTT, so the
// upload fix above stands; but the next concurrent stream moves off a tunnel
// already carrying n streams unless the alternative's RTT is more than (n+1)×
// worse. Against a 40 ms tunnel carrying one stream (score 80) an idle 60 ms
// tunnel takes the second stream and an idle 100 ms one does not; once the
// 40 ms tunnel carries two (score 120) the 100 ms tunnel takes the third.
// Without the factor every simultaneous lone stream stacked on the single
// lowest-RTT tunnel: measured 2026-09-17, two concurrent 50 MB downloads of
// different objects over a two-tunnel client (Amsterdam + Atlanta, no range
// split) put >99.5 % of the 100 MB on one transport in 3 of 3 trials and
// summed 5.07 / 8.90 / 9.10 MB/s, where one stream per tunnel gives ~10.4.
//
// A RANGE CHUNK (pickRecv) is capacity-weighted, because it is one of several
// parallel streams whose whole point is to fill the pipe: the pick minimizes
// (open streams + 1) / download capacity, i.e. the tunnel that would give the
// new chunk the most bandwidth. A tunnel with nothing proven yet is credited
// the best known capacity so it gets probed; with nothing proven anywhere
// (cold start, or the single-tunnel case) the pick is the plain fewest-streams
// rule.
//
// While a transfer is in progress (some tunnel busy), an idle tunnel whose
// estimate is stale is credited the best known capacity too: its estimate
// came from an old window (a chunk still in slow start, say) and the only way
// to refresh it is to give it a stream. Without this a tunnel once measured
// slow was never picked again, so never measured again — a range split over
// three tunnels put every byte on one. With every tunnel idle (a lone upload,
// a browser connection) the stale estimates are still the best information
// there is, and the pick weighs them as proven.
//
// A tunnel whose last exit open timed out is skipped for exitOpenPenalty the
// same way a closed tunnel is, unless it is the only live one — otherwise the
// stale-idle credit above steers the next stream straight back onto the tunnel
// that just failed to deliver a byte.
// pickSessionFor picks for a LONE stream of the given shape: it never takes a
// standby's audition offer. pickSessionKind is the form that says otherwise.
func (c *Client) pickSessionFor(dir pickDir) *yamux.Session {
	return c.pickSessionKind(dir, pickLone)
}

// pickSessionKind is pickSessionFor with the stream's kind (pickKind) spelled
// out: only a SIBLING stream — one of several parallel chunk streams — may
// consume a standby tunnel's audition offer.
func (c *Client) pickSessionKind(dir pickDir, kind pickKind) *yamux.Session {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	if len(c.sessions) == 0 {
		return nil
	}
	now := time.Now()
	counts := make([]int, len(c.sessions))
	caps := make([]float64, len(c.sessions))
	fresh := make([]bool, len(c.sessions))
	rtts := make([]float64, len(c.sessions))
	rttOK := make([]bool, len(c.sessions))
	best := 0.0
	anyBusy := false
	// A tunnel whose last exit open timed out is benched for exitOpenPenalty and
	// skipped exactly like a closed one — but only while another live tunnel can
	// take the stream. With no unbenched tunnel left the bench is ignored: a
	// benched tunnel that is all there is still beats refusing to pick.
	//
	// A STANDBY tunnel sits out on exactly the same terms. It is held open,
	// pinged and measured precisely so it can be switched in, so the escape
	// matters as much as the skip: once no active tunnel is live, the pool is
	// what the proxy runs on rather than a reason to fail.
	//
	// The ONE exception is an audition (tunnel_promoter.go): the promoter may
	// offer ONE stream to a standby tunnel whose capacity has never been
	// measured, because a standby tunnel otherwise has only a ping and the
	// promoter has nothing to check its ranking against. The stream was going
	// to be carried by some tunnel anyway, so the measurement is free.
	//
	// Only a SIBLING stream may take that offer. The idleness that makes an
	// audition free is tested once, at arm time — armAudition offers nothing
	// unless every tunnel, standby ones included, is idle — and the stream that
	// consumes the offer must be one the transfer can absorb: one of N parallel
	// chunks, never the entry stream the browser's first byte comes through.
	var auditioning *yamux.Session
	if kind == pickSibling {
		auditioning = c.auditionPickLocked(now)
	}
	sitOut := make([]bool, len(c.sessions))
	spare := 0
	for i, s := range c.sessions {
		if s == nil || s.IsClosed() {
			continue
		}
		if c.standby[s] && s != auditioning {
			sitOut[i] = true
			continue
		}
		// A SNUBBED tunnel sits out on the same terms as a benched one, and for
		// the same reason: it held work and delivered nothing, so the stale-idle
		// credit below would steer the next chunk straight back onto it. A
		// tunnel on its post-hold PROBE sits out only once it holds that one
		// chunk — the probe is how it earns its share back.
		if m := c.recvStamp[s]; m != nil && (m.onBench(now) || m.snubSitOut()) {
			sitOut[i] = true
			continue
		}
		spare++
	}
	// An audition is an instruction, not a preference: the point is to measure
	// this tunnel, and leaving the choice to the RTT rule would just pick the
	// active tunnel again.
	if auditioning != nil && !auditioning.IsClosed() {
		if m := c.recvStamp[auditioning]; m != nil {
			m.sample(now, false) // start its sample window at the stream, not before
		}
		return auditioning
	}
	for i, s := range c.sessions {
		if s == nil || s.IsClosed() || (sitOut[i] && spare > 0) {
			counts[i] = -1
			continue
		}
		counts[i] = s.NumStreams()
		anyBusy = anyBusy || counts[i] > 0
		if m := c.recvStamp[s]; m != nil {
			m.sample(now, counts[i] > 0)
			caps[i], fresh[i] = m.capacityDir(now, dir.up())
			if caps[i] > best {
				best = caps[i]
			}
			rtts[i], rttOK[i] = m.rtt()
		}
	}
	if dir == pickAny {
		// Lowest RTT PER OPEN STREAM wins: rtt × (open streams + 1). An
		// unmeasured tunnel sorts last; ties go to the tunnel carrying fewer
		// streams. leastLoaded's fewest-streams rule is the fallback when
		// nothing has been pinged yet.
		idx := -1
		bestRTT := 0.0
		for i, n := range counts {
			if n < 0 || !rttOK[i] {
				continue
			}
			score := rtts[i] * float64(n+1)
			if idx == -1 || score < bestRTT || (score == bestRTT && n < counts[idx]) {
				idx, bestRTT = i, score
			}
		}
		if idx >= 0 {
			return c.sessions[idx]
		}
		if idx = leastLoaded(counts); idx < 0 {
			return nil
		}
		return c.sessions[idx]
	}
	idx := leastLoaded(counts)
	if idx < 0 || best <= 0 {
		if idx < 0 {
			return nil
		}
		return c.sessions[idx]
	}
	// What a tunnel with nothing proven — or with a stale estimate while a
	// transfer runs — is credited.
	//
	// A RANGE CHUNK credits it the BEST capacity present, so it is probed: a
	// download that never gives an unproven tunnel a chunk never learns it is
	// the fast one, and the cost of being wrong is one chunk out of many
	// arriving late behind chunks that are still coming anyway.
	//
	// A STRIPED-UPLOAD CHUNK cannot pay that. A small object's chunks are all
	// admitted in one burst, so there is no later chunk to hide a bad guess
	// behind and the object finishes when its slowest chunk does. It credits
	// the MEDIAN of the measured tunnels instead — an unproven tunnel is no
	// better than the middle of the ones that have proven something. With
	// nothing measured anywhere `best` is 0 and the plain fewest-streams rule
	// above has already returned, which is equal shares.
	credit := best
	if dir.up() {
		if med := medianMeasured(caps); med > 0 {
			credit = med
		}
	}
	idx = -1
	bestScore, bestRTT := 0.0, 0.0
	for i, n := range counts {
		if n < 0 {
			continue
		}
		cp := caps[i]
		if cp <= 0 || (n == 0 && !fresh[i] && anyBusy) {
			cp = credit
		}
		rttMs := 0.0
		if rttOK[i] {
			rttMs = rtts[i]
		}
		score := float64(n+1) / cp
		// The RTT term, for the upload direction only: at EQUAL
		// capacity-per-stream the lower-RTT tunnel wins. It bites exactly where
		// the credit above leaves tunnels tied — two tunnels the median credits
		// alike — and a download's placement is left byte for byte as it was:
		// its tie is still broken on the stream count alone.
		switch {
		case idx == -1 || score < bestScore:
		case score != bestScore:
			continue
		case dir.up() && rttBeats(rttMs, bestRTT):
		case dir.up() && rttMs != bestRTT:
			continue
		case n < counts[idx]:
		default:
			continue
		}
		idx, bestScore, bestRTT = i, score, rttMs
	}
	return c.sessions[idx]
}

// rttBeats reports whether a measured RTT of ms displaces a current best of
// bestMs. An unmeasured RTT (0) never displaces a measured one, and never
// stands in for one.
func rttBeats(ms, bestMs float64) bool {
	if ms <= 0 {
		return false
	}
	return bestMs <= 0 || ms < bestMs
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
// livenessProbeInterval is the DEFAULT for how often the keepalive loop probes
// each tunnel. NewClient snapshots it into Client.probeInterval; the loop reads
// only that field. A var so tests can set it before constructing a client to
// drive that client's loop fast.
var livenessProbeInterval = 15 * time.Second

// tunnelRTTProbeInterval is how often the keepalive loop re-measures each
// tunnel's round-trip time for pickSessionFor(pickAny). Faster than the
// liveness probe because it steers every lone stream, and cheap: one yamux
// ping frame per tunnel.
const tunnelRTTProbeInterval = 5 * time.Second

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
// again). This is the DEFAULT: NewClient snapshots it into
// Client.hardDeadWindow and the loop reads only that field. A var so tests can
// shrink it before constructing a client. Kept comfortably above the worst
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
	ticker := time.NewTicker(c.probeInterval)
	defer ticker.Stop()

	// The RTT probe rides the same loop on its own cadence: Session.Ping already
	// returns the round-trip, so measuring the tunnel latency a lone stream is
	// picked on costs one extra ping frame per tunnel per tick.
	rttTicker := time.NewTicker(setTunnelRTTProbeInterval())
	defer rttTicker.Stop()
	rttInFlight := make(map[*yamux.Session]bool)
	rttDoneC := make(chan *yamux.Session, 64)

	// The standby pool grows on this loop too, one tunnel at a time. The
	// ticker only paces it — maybePoolFill is a no-op unless the fill is armed
	// (SetStandbyPool at start, a tunnel death after that), so after the pool
	// settles this costs a function call every setPoolFillInterval().
	poolTicker := time.NewTicker(setPoolFillInterval())
	defer poolTicker.Stop()

	// The promoter rides the same loop, on the cadence the RTT it reads is
	// refreshed at. Inert with no pool: maybePromote returns at once when
	// there is no standby tunnel or no active one.
	promoteTicker := time.NewTicker(setTunnelPromoteInterval())
	defer promoteTicker.Stop()

	// The per-tunnel SNUB is evaluated on this loop too, at half the bound so a
	// tunnel crosses it within one tick. It costs one pass over the sessions and
	// decides nothing at all while no tunnel holds outstanding work, which is
	// every tick of an idle client.
	snubTicker := time.NewTicker(snubTick())
	defer snubTicker.Stop()

	type probeResult struct {
		s   *yamux.Session
		ok  bool
		rtt time.Duration
	}
	// resC carries every ping's outcome back to the loop, even outcomes that
	// arrive long after livenessProbeTimeout (a wedge-delayed pong). Buffered so a
	// late result never blocks its goroutine when the loop is between ticks.
	resC := make(chan probeResult, 64)
	lastPong := make(map[*yamux.Session]time.Time) // last time a pong was seen (early or late)
	inFlight := make(map[*yamux.Session]bool)      // a ping is outstanding for this session
	// probes records what each tunnel's pings DID, which is the evidence a
	// retire now needs on top of the silence (tunnel_liveness.go).
	probes := newProbeLedger()
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
			probes.result(r.s, r.ok)
			if r.ok {
				lastPong[r.s] = time.Now()
				c.recordTunnelRTT(r.s, r.rtt)
			}
		case <-rttTicker.C:
			// The settings pull rides this tick: the app is the RPC client, so a
			// live knob change reaches a running client by being FETCHED here.
			// Intervals are knobs too, so a change re-cadences the tickers that
			// read them — including this one.
			if c.pullSettings() {
				rttTicker.Reset(setTunnelRTTProbeInterval())
				poolTicker.Reset(setPoolFillInterval())
				promoteTicker.Reset(setTunnelPromoteInterval())
				snubTicker.Reset(snubTick())
				ticker.Reset(c.livenessInterval())
			}
			// ...and so does the capacity-prior pull, on its own slower
			// cadence: a standby tunnel carries nothing, so the only thing
			// that can say what it might carry is the visor's per-hop
			// transport throughput.
			c.pullCapacityPriors(time.Now())
			// Refresh every live tunnel's RTT, at most one probe outstanding per
			// tunnel: a ping wedged behind a reorder gap must not pile up.
			for _, s := range c.snapshotSessions() {
				if s.IsClosed() {
					// This tick SEES the death, so this tick answers it. It
					// used to only drop the in-flight mark and leave the
					// retire to the liveness ticker, which runs at
					// probeInterval (15 s): a first-hop cut was then answered
					// 0-15 s later depending on where in that window it fell,
					// measured live at 1.2 s in one run and 13 s in the next
					// for the same code. A failover must not be a coin toss.
					c.retireTunnel(s, "tunnel session closed")
					delete(rttInFlight, s)
					continue
				}
				if rttInFlight[s] {
					continue
				}
				rttInFlight[s] = true
				go func(s *yamux.Session) {
					rtt, err := s.Ping()
					if err == nil {
						c.recordTunnelRTT(s, rtt)
					}
					select {
					case rttDoneC <- s:
					case <-c.closeC:
					}
				}(s)
			}
		case s := <-rttDoneC:
			rttInFlight[s] = false
		case <-poolTicker.C:
			c.maybePoolFill()
		case <-promoteTicker.C:
			c.maybePromote()
		case <-snubTicker.C:
			c.evaluateSnubs(time.Now())
		case <-ticker.C:
			now := time.Now()
			for _, s := range c.snapshotSessions() {
				if s.IsClosed() {
					// A tunnel that closed on its OWN — the route group was
					// torn down, the transport went away, the peer hung up —
					// is just as much an active-tunnel death as one the
					// hard-dead window catches, and is the common shape of a
					// first-hop cut. Same failover, same once-only ledger.
					c.retireTunnel(s, "tunnel session closed")
					delete(lastPong, s)
					delete(inFlight, s)
					probes.forget(s)
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
					probes.sent(s, now)
					go func(s *yamux.Session) {
						rtt, err := s.Ping()
						select {
						case resC <- probeResult{s: s, ok: err == nil, rtt: rtt}:
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
				// …and only when a PROBE has actually failed. Silence is what
				// a local dataplane stall looks like too (the visor's own
				// inbound loop froze for 30 s on 2026-09-18 and this rule
				// retired three healthy tunnels for it); a ping that errored,
				// or one outstanding past tunnel.probe_fail_window, is the
				// tunnel itself answering. See tunnel_liveness.go.
				if now.Sub(lastAlive) >= c.hardDeadWindow &&
					probes.failed(s, inFlight[s], now, c.probeFailWindow()) {
					silent := now.Sub(lastAlive).Truncate(time.Second)
					if c.appCl != nil {
						c.appCl.Log().Warnf("Liveness probe failed and no traffic either way for %v (> hard-dead window); tunnel gone, retiring it", silent)
					}
					// Closes it AND, when it was an active tunnel, promotes the
					// best standby into its place before this tick ends — so
					// the next stream has somewhere live to go immediately
					// rather than after a fresh route setup.
					c.retireTunnel(s, fmt.Sprintf("liveness: probe failed and no bytes for %v", silent))
					delete(lastPong, s)
					delete(inFlight, s)
					probes.forget(s)
				}
			}
			if c.allSessionsClosed() {
				c.close()

				return
			}
			// BACKSTOP. retireTunnel arms both of these on the death itself,
			// which is the trigger that cannot be missed; this level check
			// only catches a tunnel that left the set some other way (the
			// count dropped without a retire). It is edge-triggered on a 15 s
			// sample, so on its own it misses a death whose replacement lands
			// inside the window — which is why it is no longer the trigger.
			live := c.liveSessionCount()
			if prevLive >= 0 && live < prevLive {
				c.resetRedialBackoff()
				c.armPoolFill()
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
	if c.rs.enabled && c.isPlainPort(target) {
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

	// Non-status: open the exit's SOCKS session now, with the buffered CONNECT
	// request pipelined behind the greeting — exactly what a chunk fetch does —
	// and then splice. The exit sees the same bytes in the same order; it just no
	// longer costs a round trip to hand them over.
	if err := c.openExitPipelined(stream, greeting, req); err != nil {
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
//
// A timeout here used to be SILENT: handleStream closed both ends without
// writing a byte and without a log line, so the browser saw a bare connection
// close after exactly the sniff window and nothing anywhere said why. It is now
// logged and counted (noteExitOpenTimeout), and the tunnel it happened on sits
// out the next picks.
func (c *Client) openExit(stream net.Conn, greeting []byte) error {
	return c.openExitPipelined(stream, greeting, nil)
}

// openExitPipelined is openExit with the bytes already known to follow the
// greeting — the browser's buffered CONNECT request — written in the SAME write,
// before the method reply is read. The exit's SOCKS5 server reads its handshake
// sequentially off the stream, so the queued CONNECT is simply the next thing it
// reads; the wire bytes and their order are identical to writing them one at a
// time, only a round trip shorter. This is what a range-split chunk fetch has
// always done (exitConnectPipelined), applied to the browser-facing first
// stream, where the prelude before the first parallel fetch was three serial
// exit round trips.
//
// Failure semantics are unchanged: a timeout or a non-no-auth method reply is
// counted and benched exactly as before and the caller closes both ends, so a
// CONNECT that was already queued is never acted on beyond the exit's own dial.
func (c *Client) openExitPipelined(stream net.Conn, greeting, pipelined []byte) error {
	started := time.Now()
	_ = stream.SetReadDeadline(started.Add(c.exitOpenWindow())) //nolint:errcheck
	head := greeting
	if len(pipelined) > 0 {
		head = make([]byte, 0, len(greeting)+len(pipelined))
		head = append(head, greeting...)
		head = append(head, pipelined...)
	}
	if _, err := stream.Write(head); err != nil {
		return err
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(stream, method); err != nil {
		if isTimeout(err) {
			c.noteExitOpenTimeout(stream, time.Since(started), err)
		}
		return err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return fmt.Errorf("exit selected non-no-auth method %v", method)
	}
	// The exit answered: whatever benched this tunnel is over.
	if m, _ := c.tunnelOf(stream); m != nil {
		m.unbench()
	}
	return nil
}

// exitOpenWindow is how long openExit waits for the exit's method-selection
// reply: statusSniffTimeout unless this client was given a shorter one.
func (c *Client) exitOpenWindow() time.Duration {
	if c.sniffTimeout > 0 {
		return c.sniffTimeout
	}
	return statusSniffTimeout
}

// isTimeout reports whether err is a deadline expiry rather than a real error.
// yamux surfaces its own ErrTimeout for an expired stream deadline, which
// satisfies net.Error; os.ErrDeadlineExceeded covers a plain net.Conn.
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// tunnelOf returns the meter of the tunnel a stream was opened on, and that
// tunnel's index in the current set (-1 when unknown). Best-effort: a stream
// that is not a *yamux.Stream of one of our sessions — the in-memory pipes some
// tests splice through — yields (nil, -1) and is simply not charged.
func (c *Client) tunnelOf(stream net.Conn) (*tunnelMeter, int) {
	sr, ok := stream.(interface{ Session() *yamux.Session })
	if !ok {
		return nil, -1
	}
	sess := sr.Session()
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	for i, s := range c.sessions {
		if s == sess {
			return c.recvStamp[s], i
		}
	}
	return nil, -1
}

// noteExitOpenTimeout makes a silent exit-open timeout visible: a WARN naming
// the tunnel and how long it waited, a cumulative counter the status page
// renders, and a short bench on the tunnel so the next stream tries another one.
func (c *Client) noteExitOpenTimeout(stream net.Conn, elapsed time.Duration, err error) {
	c.exitOpenTimeouts.Add(1)
	m, idx := c.tunnelOf(stream)
	if m != nil {
		m.bench(time.Now())
	}
	if c.appCl == nil {
		return
	}
	tunnel := "tunnel ?"
	if idx >= 0 {
		tunnel = fmt.Sprintf("tunnel %d/%d", idx+1, len(c.snapshotSessions()))
	}
	c.appCl.Log().Warnf("Exit never answered the SOCKS5 greeting on %s after %s (%v); benching it for %s — %d exit-open timeout(s) so far",
		tunnel, elapsed.Round(100*time.Millisecond), err, setExitOpenPenalty(), c.exitOpenTimeouts.Load())
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
		StreamsPerSplit: c.rsConcurrency(),
		ChunkSize:       c.rsChunkSize(),
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
		// The GPU route-graph view's engine: the one skywire command module run
		// in its "netview" role (it publishes the generic cosmos-go graph API the
		// page drives), served same-origin so the page's strict self-contained
		// context can instantiate it. Same module pkg/tpviz serves at
		// /tpviz-gl.wasm; served here straight from the copy the native binary
		// embeds (pkg/wasmhv/execwasm). In-process, no exit round-trip.
		stream.Close() //nolint:errcheck,gosec
		writeStatusWasmResponse(conn)
		return
	case "/wasm_exec.js":
		stream.Close()                              //nolint:errcheck,gosec
		_, _ = conn.Write(statusWasmExecResponse()) //nolint:errcheck
		return
	}

	body := proxystatus.Render(c.statusSnapshot())
	_, _ = conn.Write(statusHTTPResponse(body)) //nolint:errcheck
}

// writeStatusWasmResponse writes the raw HTTP/1.1 response carrying the
// skywire command module for /main.wasm, out of the copy embedded in the
// native binary (pkg/wasmhv/execwasm). It serves the gzipped bytes verbatim
// with Content-Encoding: gzip (the browser inflates;
// WebAssembly.instantiateStreaming is happy with the result), avoiding
// inflating megabytes per request. A 503 is returned when no module is
// embedded — a source build without `make embed-exec-wasm`, or the js build,
// which embeds nothing (it IS the module) — in which case the page silently
// keeps the ASCII tree view.
//
// The module streams from the binary's read-only mapping straight to the
// connection: asking execwasm for the bytes would copy ~37 MB onto the heap
// per request.
func writeStatusWasmResponse(w io.Writer) {
	n := execwasm.Size()
	if n == 0 {
		_, _ = w.Write(statusServiceUnavailable("no skywire.wasm module embedded in this build")) //nolint:errcheck
		return
	}
	f, err := execwasm.Open()
	if err != nil {
		_, _ = w.Write(statusServiceUnavailable("no skywire.wasm module embedded in this build")) //nolint:errcheck
		return
	}
	defer f.Close() //nolint:errcheck
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: application/wasm\r\n")
	b.WriteString("Content-Encoding: gzip\r\n")
	fmt.Fprintf(&b, "ETag: %q\r\n", execwasm.Stamp())
	fmt.Fprintf(&b, "Content-Length: %d\r\n", n)
	b.WriteString("Cache-Control: no-store\r\nConnection: close\r\n\r\n")
	if _, err := w.Write(b.Bytes()); err != nil {
		return
	}
	_, _ = io.Copy(w, f) //nolint:errcheck
}

// statusWasmExecResponse returns the raw HTTP/1.1 response for /wasm_exec.js —
// Go's loader (pkg/wasmhv) pinned to the module's netview role (argv + env,
// execwasm.LoaderJS); the page sets the same argv itself
// (pkg/proxystatus/render.go). Small, so served uncompressed.
func statusWasmExecResponse() []byte {
	js := execwasm.LoaderJS(wasmhv.WasmExecJS, "netview")
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
	// Exit-open timeouts are local client truth too, and the one failure the page
	// could not previously show at all: the tunnel is up, the browser gets
	// nothing. Rendered in the note so it is visible without a JSON reader.
	if n := c.exitOpenTimeouts.Load(); n > 0 {
		snap.ExitOpenTimeouts = n
		snap.Note = fmt.Sprintf("%s · exit-open timeouts: %d", snap.Note, n)
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
