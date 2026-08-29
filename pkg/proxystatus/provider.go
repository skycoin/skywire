// Package proxystatus pkg/proxystatus/provider.go c4-app-web
//
// A read-only, self-contained diagnostic page each native mesh proxy serves at
// a reserved, well-known host through itself — the proxy counterpart of the
// branded route interstitial (pkg/proxyinterstitial). Hitting one of the
// reserved hosts through a resolving proxy shows that surface's live state:
//
//   - http://status.dmsg/      → the dmsg resolving proxy (dmsg_web)
//   - http://status.skynet/    → the skynet resolving proxy (skynet_web)
//   - http://status.skysocks/  → the skysocks-client SOCKS tunnel
//
// Injection model mirrors the resolver's reserved "home" host: the SOCKS5 Dial
// callback recognizes a status host BEFORE any suffix match / upstream forward,
// renders the page in-process (nothing is dialed out over the mesh) and splices
// the rendered HTTP response back to the browser via ServeConn.
//
// MVP scope is READ-ONLY: the three data sections a proxy operator wants when a
// route misbehaves — recent per-process log lines, route/transport events, and a
// mux-plot-style per-leg bandwidth/RTT view of the surface's route group (the
// same RouteGroupMuxInfo telemetry `cli proxy mux plot` renders in the
// terminal). Route CONTROL (change/select routes, pick a dmsg relay) is left as
// an explicit, clearly-marked seam — see render.go's control section and the
// Snapshot doc — so it can be added later without reshaping the wire.
package proxystatus

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Surface identifies which proxy a status page describes. The string value is
// the label in the reserved host (status.<surface>) and the human name shown on
// the page.
type Surface string

// The three proxy surfaces that serve a status page.
const (
	SurfaceDmsg     Surface = "dmsg"
	SurfaceSkynet   Surface = "skynet"
	SurfaceSkysocks Surface = "skysocks"
)

// statusLabel is the reserved first label of every status host.
const statusLabel = "status"

// Leg is one route in a surface's mux'd route group, decoupled from
// visor.MuxLegInfo so this package doesn't import pkg/visor (which would be a
// cycle — the visor implements Provider). Fields mirror the RouteGroupMuxInfo
// telemetry the `cli proxy mux plot` renderer consumes.
type Leg struct {
	Index       int
	TransportID string
	TpType      string
	RemotePK    string
	// LatencyMS is the first-hop transport RTT; RouteLatencyMS is the leg's
	// true end-to-end route latency (all hops). Direct is true for a 1-hop
	// route to the destination, false for a multihop (relayed) leg.
	LatencyMS      float64
	RouteLatencyMS float64
	Direct         bool
	SentBytes      uint64
	RecvBytes      uint64
	Retransmits    uint64
	// GoodputBps is this leg's recent goodput — the EWMA of (sent+recv) bytes
	// per second over the page's refresh window (~1s), i.e. the RATE the leg is
	// moving now, distinct from the cumulative SentBytes/RecvBytes counters. 0
	// until a second sample lands. GoodputUpBps/GoodputDownBps split it by
	// direction (send-rate / recv-rate), shown beside the ↑/↓ byte totals and
	// driving the per-route share bars; GoodputBps is their sum.
	GoodputBps     float64
	GoodputUpBps   float64
	GoodputDownBps float64
	Alive          bool
	Standby        bool
	// Hops is the leg's full forward route (every hop to the destination),
	// full PKs, per-hop transport type + latency where known.
	Hops []Hop
}

// Hop is one hop of a leg's forward route. From/To are FULL public keys —
// the status page never truncates them. LatencyMS is the hop's transport
// RTT where known (first hop owned; single-intermediate far hop derived).
type Hop struct {
	TpID      string
	From      string
	To        string
	TpType    string
	LatencyMS float64
}

// Snapshot is everything a status page renders for one surface. It is a
// point-in-time projection; the page meta-refreshes to re-fetch, so a stale
// Snapshot is self-correcting rather than load-bearing.
//
// Extension seam (route control): a future write-capable status page adds a
// Controls section here (candidate legs, current scheduler mode, selectable dmsg
// relays) plus a matching mutating method on Provider. The read-only fields
// below are already shaped so a control UI can render alongside them without a
// wire change. Keep additions additive.
type Snapshot struct {
	Surface    Surface
	App        string // underlying app/logger name (dmsgweb / skynetweb / skysocks-client)
	Running    bool   // whether the surface's process/runtime is currently alive
	MuxEnabled bool   // route group has multiplexing enabled
	Legs       []Leg  // current per-leg mux state (empty when no active route group)
	// Tunnels is the STREAM-level view: one entry per active route group (a
	// --tunnels stream), each carrying its own PACKET-level mux Legs. The two
	// levels of route multiplexing are stream (tunnels, disjoint route groups)
	// over packet (mux legs striping within a group). When populated the route
	// tree nests tunnel → legs; when empty the tree falls back to the flat Legs
	// list above (a caller that only projects a single route group). Legs mirrors
	// Tunnels[0].Legs for back-compat.
	Tunnels []Tunnel
	Logs    []string // recent log lines, oldest first
	Events  []string // route/transport events affecting this surface, oldest first
	Streams []Stream // per-stream detail for the open session (skysocks tunnel), when tracked
	Note    string   // optional human note (e.g. why a section is empty)
}

// Tunnel is one STREAM-level route group — a single --tunnels stream to the exit,
// carrying its own PACKET-level mux Legs. With --tunnels N there are N tunnels,
// each auto-steered onto a disjoint first-hop transport; with a single tunnel
// there is one entry whose Legs are the mux's packet-striped routes.
type Tunnel struct {
	Index      int    // tunnel index in dial order (0-based)
	ExitPK     string // destination exit PK (full, never truncated); same across tunnels
	MuxEnabled bool   // this tunnel's route group has packet-level mux enabled
	Legs       []Leg  // this tunnel's packet-level mux legs
}

// Stream is one open tunneled stream on the surface's session to the exit — the
// per-stream detail behind the "N open stream(s)" count. The skysocks-client
// tracks these locally: id + CONNECT target + age, plus the stream's own up/down
// byte counters and a smoothed transfer rate. The counters are metered by the
// splice loop that carries the stream (a counting wrapper on the yamux conn), so
// they are true per-stream totals, distinct from the route-group-wide sent/recv
// totals in the mux section.
type Stream struct {
	ID          uint32  // yamux stream id
	Target      string  // the CONNECT target host:port the stream carries (never a PK)
	AgeMS       int64   // how long the stream has been open, milliseconds
	SentBytes   uint64  // cumulative bytes browser→exit (up) on this stream
	RecvBytes   uint64  // cumulative bytes exit→browser (down) on this stream
	SentRateBps float64 // smoothed up rate in bytes/sec (EWMA over the refresh window); 0 until a second sample lands
	RecvRateBps float64 // smoothed down rate in bytes/sec (EWMA over the refresh window)
	// LatencyMS is the ROUTE-GROUP latency of the stream's session to the exit,
	// NOT a per-stream RTT — yamux exposes no per-stream round-trip. It is the
	// representative route latency of the surface's live legs, attached to every
	// stream and LABELED as route-group latency by the renderer rather than faked
	// as a per-stream number. 0 when unmeasured.
	LatencyMS float64
}

// Provider yields a live Snapshot for a surface. Implemented by the visor
// (pkg/visor/embedded_proxystatus.go), injected into the resolving-proxy
// runtimes' Config. A nil Provider disables the status hosts entirely.
type Provider interface {
	StatusSnapshot(surface Surface) (Snapshot, error)
}

// Match reports whether host is one of the reserved status hosts and, if so,
// which surface it names. host is the raw request host (an optional :port is
// tolerated and ignored). Matching is case-insensitive and exact on the two
// labels — "status.skysocks" matches but "x.status.skysocks" and "status" alone
// do not, so a status host never shadows a real <vhost>.<pk>.<suffix> lookup.
func Match(host string) (Surface, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i+1:], ".") {
		h = h[:i] // strip a trailing :port (never a dotted label)
	}
	label, rest, ok := strings.Cut(h, ".")
	if !ok || label != statusLabel {
		return "", false
	}
	switch Surface(rest) {
	case SurfaceDmsg, SurfaceSkynet, SurfaceSkysocks:
		return Surface(rest), true
	default:
		return "", false
	}
}

// Host returns the reserved status host for a surface ("status.skysocks"), for
// links (the interstitial's "view status" affordance) and tests.
func Host(s Surface) string { return statusLabel + "." + string(s) }

// ServeConn returns one end of an in-memory pipe; the other end is fed body as a
// single HTTP/1.1 response then closed. Nothing is dialed out — the page exists
// only as seen THROUGH the proxy. The returned conn is handed to go-socks5,
// which pipes the browser's request in and the response back. Mirrors dmsgweb's
// serveHomeInProcess so the two reserved in-process hosts behave identically.
func ServeConn(body []byte) net.Conn {
	srvConn, cliConn := net.Pipe()
	go func() {
		defer srvConn.Close() //nolint:errcheck
		br := bufio.NewReader(srvConn)
		// Consume the request line + headers so the peer's write completes; the
		// path is ignored (the status page is the same for every path).
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		var resp strings.Builder
		resp.WriteString("HTTP/1.1 200 OK\r\n")
		resp.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		fmt.Fprintf(&resp, "Content-Length: %d\r\n", len(body))
		resp.WriteString("Cache-Control: no-store\r\n")
		resp.WriteString("Connection: close\r\n\r\n")
		if _, err := srvConn.Write([]byte(resp.String())); err != nil {
			return
		}
		_, _ = srvConn.Write(body) //nolint:errcheck // best-effort; peer may have gone
	}()
	return cliConn
}
