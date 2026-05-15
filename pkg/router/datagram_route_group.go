// Package router pkg/router/datagram_route_group.go: faithful-UDP-
// over-skynet sibling to RouteGroup. Stage 2 of #2607.
//
// While RouteGroup is net.Conn-shaped (Read/Write, sequenced
// DataPackets, mux + reorder buffer + SACK retransmit, head-of-line
// blocking on loss), DatagramRouteGroup is net.PacketConn-shaped
// (ReadFrom/WriteTo, unsequenced DatagramPackets, loss-tolerant,
// possibly-unordered delivery). Used by Stage 4's
// forwarded_ports.udp path to expose UDP datagram semantics over
// routed skynet hops — for VoIP, gaming, WebRTC media, anything
// where a late packet is worse than a lost one.
//
// Stage 2 scope: type definition, send/receive plumbing,
// net.PacketConn methods, lifecycle. NO crypto wrapping yet
// (Stage 3 will add per-datagram ChaCha20-Poly1305-IETF + sliding-
// window anti-replay). NO router-side dispatch yet (Stage 4 will
// wire forwarded_ports.udp). At this stage DatagramRouteGroup is
// self-contained and unit-testable, but no packet in the running
// system will reach it.

package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/deadline"
)

// Group is the narrow interface every route-group variant shares.
// Stream-shaped (RouteGroup, net.Conn) and datagram-shaped
// (DatagramRouteGroup, net.PacketConn) both satisfy it. Read/Write
// vs ReadFrom/WriteTo deliberately stay on the concrete types —
// keeping the shared interface narrow avoids leaking stream vs
// datagram semantics into call sites that only care about
// lifecycle.
type Group interface {
	Close() error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	IsAlive() bool
}

// MaxDatagramPayload is the largest datagram body that fits in a
// single DatagramPacket on the wire. The packet header carries a
// uint16 payload-size field, capping the total payload (including
// any Stage 3 AEAD overhead) at 65535 bytes. Apps consult
// DatagramRouteGroup.MaxPayload() rather than this constant so the
// answer accounts for the AEAD tag + nonce overhead Stage 3 will
// reserve.
const MaxDatagramPayload = math.MaxUint16

// ErrPayloadTooBig is returned by WriteTo when the caller provides
// a datagram larger than MaxPayload. Mirrors net.UDPConn's behavior
// on oversized sends but is surfaced explicitly rather than as an
// opaque EMSGSIZE — apps that consulted MaxPayload() should never
// see this in practice.
var ErrPayloadTooBig = errors.New("datagram-route-group: payload exceeds MaxPayload")

// ErrClosed is returned by ReadFrom / WriteTo after Close.
var ErrClosed = errors.New("datagram-route-group: closed")

// DatagramRouteGroup carries UDP-shaped datagrams across a route
// (or set of routes — multiplexing across multiple paths is a
// future enhancement, not present in Stage 2). Single-peer:
// WriteTo only accepts the configured remote address; ReadFrom
// always returns the same remote.
//
// Lifecycle:
//   - Constructed via NewDatagramRouteGroup with rules + transports
//     (the same setup-node plumbing that builds RouteGroups can
//     populate either type — Stage 4 wires this).
//   - Handle(packet) is called by the router whenever a
//     DatagramPacket arrives whose RouteID matches one of this
//     group's rules. The packet's payload is queued on readCh and
//     delivered to the next ReadFrom caller.
//   - WriteTo wraps the payload in a DatagramPacket (Stage 3 will
//     also AEAD-seal it), picks a transport, and writes it.
//   - Close releases the readCh and marks the group dead. Idempotent.
type DatagramRouteGroup struct {
	// atomic; first for 64-bit alignment.
	lastSent     int64
	closeFlag    int32 // atomic; 1 once Close has run
	consecutiveWriteFailures int32

	mu sync.Mutex

	cfg    *RouteGroupConfig
	logger *logging.Logger
	desc   routing.RouteDescriptor
	rt     routing.Table

	// tps[i] is the managed transport over which writes to fwd[i]
	// land. Same shape as RouteGroup.tps — keeping the indexing
	// invariant identical means the router-side setup code (Stage 4)
	// can build either group type with the same rule/transport pair.
	tps []*transport.ManagedTransport
	fwd []routing.Rule // forward rules (for writing)
	rvs []routing.Rule // reverse rules (for reading)

	// readCh delivers datagram payloads to ReadFrom. Buffered to
	// cfg.ReadChBufSize so a brief ReadFrom stall doesn't immediately
	// drop incoming traffic — but it IS a bounded queue, and once
	// full further inbound datagrams are dropped (with a logged
	// counter). This is the right semantics for UDP: an app that
	// isn't keeping up loses packets, exactly as it would on a
	// local UDP socket.
	readCh chan []byte

	readDeadline  deadline.PipeDeadline
	writeDeadline deadline.PipeDeadline

	networkStats *networkStats

	closed chan struct{}
	once   sync.Once

	// inboundDropped counts datagrams dropped because readCh was
	// full when Handle tried to push. Surfaced via Stats() and
	// useful for diagnosing slow consumers.
	inboundDropped uint64 // atomic
}

// NewDatagramRouteGroup constructs a DatagramRouteGroup. The
// returned group has no rules or transports yet — those are added
// by the caller (the setup-node-side plumbing, Stage 4) before the
// group is registered with the router for dispatch.
//
// Use cfg=nil for defaults.
func NewDatagramRouteGroup(cfg *RouteGroupConfig, rt routing.Table, desc routing.RouteDescriptor, mLogger *logging.MasterLogger) *DatagramRouteGroup {
	if cfg == nil {
		cfg = DefaultRouteGroupConfig()
	}
	logger := logging.MustGetLogger(fmt.Sprintf("DatagramRouteGroup %s", desc.String()))
	if mLogger != nil {
		logger = mLogger.PackageLogger(fmt.Sprintf("DatagramRouteGroup %s", desc.String()))
	}
	return &DatagramRouteGroup{
		cfg:           cfg,
		logger:        logger,
		desc:          desc,
		rt:            rt,
		tps:           make([]*transport.ManagedTransport, 0),
		fwd:           make([]routing.Rule, 0),
		rvs:           make([]routing.Rule, 0),
		readCh:        make(chan []byte, cfg.ReadChBufSize),
		readDeadline:  deadline.MakePipeDeadline(),
		writeDeadline: deadline.MakePipeDeadline(),
		networkStats:  newNetworkStats(),
		closed:        make(chan struct{}),
	}
}

// AddRule registers a (forward rule, transport, reverse rule)
// triple. Called by the setup-node integration in Stage 4. Index
// invariant: tps[i] is the transport for fwd[i]; rvs[i] is the
// matching consume rule on the receive side.
//
// In Stage 2 this is the only way to populate the group; the router-
// side automatic plumbing comes later.
func (dg *DatagramRouteGroup) AddRule(fwd routing.Rule, tp *transport.ManagedTransport, rv routing.Rule) {
	dg.mu.Lock()
	defer dg.mu.Unlock()
	dg.fwd = append(dg.fwd, fwd)
	dg.tps = append(dg.tps, tp)
	dg.rvs = append(dg.rvs, rv)
}

// MaxPayload returns the largest datagram size that WriteTo will
// accept. In Stage 2 this is the raw wire cap (65535). Stage 3
// will reduce it by the per-datagram AEAD overhead (8-byte nonce
// counter + 16-byte Poly1305 tag = 24 bytes), at which point
// MaxPayload returns 65511. Apps that consult this before sending
// avoid the opaque-EMSGSIZE footgun.
func (dg *DatagramRouteGroup) MaxPayload() int {
	return MaxDatagramPayload
}

// WriteTo wraps data in a DatagramPacket and sends it over the
// first available transport. addr is checked against the group's
// remote (single-peer model); WriteTo on a wrong addr returns an
// error rather than silently routing elsewhere — apps that use a
// DatagramRouteGroup as a single-peer net.PacketConn are unlikely
// to hit this, but app frameworks that pool PacketConns might.
func (dg *DatagramRouteGroup) WriteTo(data []byte, addr net.Addr) (int, error) {
	if dg.isClosed() {
		return 0, ErrClosed
	}
	if len(data) > dg.MaxPayload() {
		return 0, ErrPayloadTooBig
	}
	if addr != nil {
		if want := dg.RemoteAddr(); want != nil && addr.String() != want.String() {
			return 0, fmt.Errorf("datagram-route-group: WriteTo addr %s does not match group remote %s", addr, want)
		}
	}

	dg.mu.Lock()
	if len(dg.fwd) == 0 || len(dg.tps) == 0 {
		dg.mu.Unlock()
		return 0, errors.New("datagram-route-group: no rules / transports configured")
	}
	rule := dg.fwd[0]
	tp := dg.tps[0]
	dg.mu.Unlock()

	packet, err := routing.MakeDatagramPacket(rule.NextRouteID(), data)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	select {
	case <-dg.writeDeadline.Wait():
		return 0, timeoutError{}
	case <-dg.closed:
		return 0, ErrClosed
	default:
	}

	if err := tp.WritePacket(ctx, packet); err != nil {
		atomic.AddInt32(&dg.consecutiveWriteFailures, 1)
		return 0, err
	}
	atomic.StoreInt32(&dg.consecutiveWriteFailures, 0)
	atomic.StoreInt64(&dg.lastSent, time.Now().UnixNano())
	dg.networkStats.AddBandwidthSent(uint64(packet.Size()))
	return len(data), nil
}

// ReadFrom delivers the next inbound datagram payload. Blocks until
// a payload is available, the read deadline fires, or Close runs.
// The returned addr is always the group's remote (single-peer
// model). Buffer too small for the datagram is a partial-read
// situation: copy what fits, drop the rest, return the truncated
// length and no error — exactly net.UDPConn's behavior on
// undersized buffers (subject to ENOBUFS-equivalent surfacing at
// the Stage 5 app-API layer).
func (dg *DatagramRouteGroup) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data, ok := <-dg.readCh:
		if !ok {
			return 0, dg.RemoteAddr(), io.EOF
		}
		n := copy(p, data)
		return n, dg.RemoteAddr(), nil
	case <-dg.readDeadline.Wait():
		return 0, dg.RemoteAddr(), timeoutError{}
	case <-dg.closed:
		return 0, dg.RemoteAddr(), ErrClosed
	}
}

// Handle is called by the router (Stage 4) when a DatagramPacket
// arrives whose RouteID matches one of dg.rvs. The payload is
// queued on readCh and delivered to the next ReadFrom. If readCh
// is full, the datagram is dropped (atomic inboundDropped++) —
// faithful UDP semantics: a slow consumer loses packets.
//
// Returns nil on successful queue, io.ErrClosedPipe if the group
// is closed (router should de-register the route in that case).
func (dg *DatagramRouteGroup) Handle(packet routing.Packet) error {
	if dg.isClosed() {
		return io.ErrClosedPipe
	}
	if packet.Type() != routing.DatagramPacket {
		return fmt.Errorf("datagram-route-group: Handle received non-datagram %s", packet.Type())
	}
	dg.networkStats.AddBandwidthReceived(uint64(packet.Size()))

	// Copy out the payload — the underlying packet buffer is owned
	// by the read loop and may be reused for the next inbound
	// frame. Without the copy a fast inbound rate would clobber
	// queued datagrams before the app reads them.
	payload := packet.Payload()
	cp := make([]byte, len(payload))
	copy(cp, payload)

	select {
	case dg.readCh <- cp:
		return nil
	case <-dg.closed:
		return io.ErrClosedPipe
	default:
		atomic.AddUint64(&dg.inboundDropped, 1)
		return nil
	}
}

// Close releases the readCh and marks the group dead. Idempotent.
// In-flight ReadFrom unblocks with ErrClosed; subsequent WriteTo
// also returns ErrClosed.
func (dg *DatagramRouteGroup) Close() error {
	dg.once.Do(func() {
		atomic.StoreInt32(&dg.closeFlag, 1)
		close(dg.closed)
		// Drain readCh non-blockingly so any in-flight Handle that
		// raced the close doesn't leak.
		go func() {
			for range dg.readCh {
			}
		}()
		// Closing readCh lets the drain goroutine exit. Order
		// matters: close after signaling dg.closed so any select
		// observers see the closed signal first.
		close(dg.readCh)
	})
	return nil
}

func (dg *DatagramRouteGroup) isClosed() bool {
	return atomic.LoadInt32(&dg.closeFlag) == 1
}

// LocalAddr returns destination address of underlying
// RouteDescriptor. Mirrors RouteGroup's convention — the descriptor
// is built from the dialer's perspective so .Dst is "this side."
func (dg *DatagramRouteGroup) LocalAddr() net.Addr {
	return dg.desc.Dst()
}

// RemoteAddr returns source address of underlying RouteDescriptor.
func (dg *DatagramRouteGroup) RemoteAddr() net.Addr {
	return dg.desc.Src()
}

// SetDeadline sets both read and write deadlines.
func (dg *DatagramRouteGroup) SetDeadline(t time.Time) error {
	if err := dg.SetReadDeadline(t); err != nil {
		return err
	}
	return dg.SetWriteDeadline(t)
}

// SetReadDeadline sets the read deadline.
func (dg *DatagramRouteGroup) SetReadDeadline(t time.Time) error {
	dg.readDeadline.Set(t)
	return nil
}

// SetWriteDeadline sets the write deadline.
func (dg *DatagramRouteGroup) SetWriteDeadline(t time.Time) error {
	dg.writeDeadline.Set(t)
	return nil
}

// IsAlive returns true if the group is open and recent writes have
// succeeded. Mirror of RouteGroup.IsAlive's shape so the Group
// interface stays narrow.
func (dg *DatagramRouteGroup) IsAlive() bool {
	if dg.isClosed() {
		return false
	}
	return atomic.LoadInt32(&dg.consecutiveWriteFailures) < maxConsecutiveWriteFailures
}

// InboundDropped returns the count of inbound datagrams dropped
// due to a full readCh since group construction. Surfaced for
// diagnostics; not used in the hot path.
func (dg *DatagramRouteGroup) InboundDropped() uint64 {
	return atomic.LoadUint64(&dg.inboundDropped)
}

// Compile-time assertion that DatagramRouteGroup satisfies the
// narrow Group interface AND net.PacketConn.
var (
	_ Group          = (*DatagramRouteGroup)(nil)
	_ net.PacketConn = (*DatagramRouteGroup)(nil)
)
