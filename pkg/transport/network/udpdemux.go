//go:build !tinygo

// Package network pkg/transport/network/udpdemux.go
//
// udpDemux multiplexes ONE UDP socket across the UDP-based transport protocols
// (QUIC, sudph/KCP, WebRTC/ICE), so a visor can listen for all of them on a
// single `transport_port` instead of one port per type. See
// docs/design/transport-port-unification.md.
//
// Each inbound datagram is classified by its wire signature (classifyUDP) and
// routed to a per-protocol virtual net.PacketConn (demuxConn). The protocols'
// own stacks — quic-go's Transport, xtaci/kcp-go, pion's ICE UDP-mux — each take
// a net.PacketConn, so they consume a demuxConn unchanged. Classification is
// applied to the FIRST datagram from a remote address; the result is then PINNED
// per source (srcProto) so short-header QUIC and subsequent KCP packets — which
// carry no distinguishing prefix — follow the same route.
//
// This component is intentionally standalone + unit-tested before being wired
// into the transport managers; getting the classifier wrong would misroute live
// traffic, so it is validated in isolation first.
package network

import (
	"encoding/binary"
	"net"
	"sync"
	"time"
)

// udpProto identifies a UDP-based transport protocol on the shared socket.
type udpProto uint8

const (
	protoUnknown udpProto = iota
	protoQUIC
	protoWebRTC
	protoSUDPH
)

func (p udpProto) String() string {
	switch p {
	case protoQUIC:
		return "quic"
	case protoWebRTC:
		return "webrtc"
	case protoSUDPH:
		return "sudph"
	default:
		return "unknown"
	}
}

const (
	// stunMagicCookie is the fixed value at bytes 4..8 of every STUN message
	// (RFC 5389) — WebRTC's ICE connectivity checks. Unambiguous.
	stunMagicCookie = 0x2112A442
	// KCP command byte (offset 4) values (xtaci/kcp-go: IKCP_CMD_PUSH..WINS).
	kcpCmdMin = 81
	kcpCmdMax = 84
)

// classifyUDP returns the protocol of a datagram from its leading bytes:
//
//   - WebRTC: a STUN message (two high bits of byte 0 clear + magic cookie at
//     4..8) or a DTLS record (content-type 20..63 in byte 0, DTLS version 0xfe**
//     in byte 1). Both are unambiguous.
//   - QUIC: a long-header packet (header-form + fixed bit set → 0xC0 mask) whose
//     KCP-command slot (byte 4) is NOT a KCP command. New QUIC connections always
//     start with a long header (Initial), whose byte 4 is the low version byte
//     (0x01 for v1), never 81..84.
//   - sudph (KCP): a packet whose command byte (offset 4) is 81..84. KCP carries
//     no magic, so this is the most specific signal it has.
//
// Returns protoUnknown when nothing matches — the caller pins a source's protocol
// from its first classifiable packet, so short-header QUIC / bare KCP follow-ups
// don't need to re-classify.
func classifyUDP(p []byte) udpProto {
	if len(p) == 0 {
		return protoUnknown
	}
	// STUN: message type's two high bits are 0; magic cookie at 4..8.
	if len(p) >= 8 && p[0]&0xC0 == 0 && binary.BigEndian.Uint32(p[4:8]) == stunMagicCookie {
		return protoWebRTC
	}
	// DTLS record: content-type 20..63, version high byte 0xfe.
	if len(p) >= 3 && p[0] >= 20 && p[0] <= 63 && p[1] == 0xfe {
		return protoWebRTC
	}
	isKCPCmd := len(p) >= 5 && p[4] >= kcpCmdMin && p[4] <= kcpCmdMax
	// QUIC long header: header-form + fixed bit (0xC0), and not a KCP command in
	// the byte-4 slot (a long-header QUIC Initial has a version byte there, not
	// 81..84).
	if p[0]&0xC0 == 0xC0 && !isKCPCmd {
		return protoQUIC
	}
	if isKCPCmd {
		return protoSUDPH
	}
	return protoUnknown
}

// udpDemux wraps one underlying net.PacketConn and fans inbound datagrams out to
// a per-protocol virtual net.PacketConn. Writes from any virtual conn go to the
// shared socket. Close on the demux closes the socket and all virtual conns.
type udpDemux struct {
	conn net.PacketConn

	mu    sync.RWMutex
	srcs  map[string]udpProto // remote addr → pinned protocol
	conns map[udpProto]*demuxConn
	done  chan struct{}
	once  sync.Once
}

// newUDPDemux starts demultiplexing conn. Call Conn(proto) to obtain the virtual
// net.PacketConn for each protocol, then hand those to the protocol stacks.
func newUDPDemux(conn net.PacketConn) *udpDemux {
	d := &udpDemux{
		conn:  conn,
		srcs:  make(map[string]udpProto),
		conns: make(map[udpProto]*demuxConn),
		done:  make(chan struct{}),
	}
	for _, p := range []udpProto{protoQUIC, protoWebRTC, protoSUDPH} {
		d.conns[p] = newDemuxConn(d, p)
	}
	go d.readLoop()
	return d
}

// Conn returns the virtual net.PacketConn carrying proto's traffic.
func (d *udpDemux) Conn(proto udpProto) net.PacketConn { return d.conns[proto] }

// route returns the protocol a datagram from src belongs to, pinning the source's
// protocol on its first classifiable packet so later (unclassifiable) packets
// from the same source follow the same route.
func (d *udpDemux) route(src net.Addr, p []byte) (udpProto, bool) {
	key := src.String()
	d.mu.RLock()
	proto, pinned := d.srcs[key]
	d.mu.RUnlock()
	if pinned {
		return proto, true
	}
	proto = classifyUDP(p)
	if proto == protoUnknown {
		return protoUnknown, false
	}
	d.mu.Lock()
	d.srcs[key] = proto
	d.mu.Unlock()
	return proto, true
}

func (d *udpDemux) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, src, err := d.conn.ReadFrom(buf)
		if err != nil {
			d.Close() //nolint:errcheck,gosec
			return
		}
		proto, ok := d.route(src, buf[:n])
		if !ok {
			continue // unclassifiable, no pinned source — drop
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		c := d.conns[proto]
		select {
		case c.in <- packet{addr: src, data: pkt}:
		case <-d.done:
			return
		default:
			// virtual conn backlogged — drop (UDP is lossy; the protocol recovers).
		}
	}
}

// Close closes the underlying socket and every virtual conn.
func (d *udpDemux) Close() error {
	d.once.Do(func() {
		close(d.done)
		for _, c := range d.conns {
			c.close()
		}
	})
	return d.conn.Close()
}

// packet is one inbound datagram queued for a virtual conn.
type packet struct {
	addr net.Addr
	data []byte
}

// demuxConn is a virtual net.PacketConn over the shared socket: ReadFrom drains
// this protocol's inbound queue (deadline-aware, and a deadline change interrupts
// a blocked read — quic-go/kcp-go rely on that); WriteTo writes straight to the
// shared socket. Write deadlines delegate to the shared socket (UDP writes don't
// block in practice).
type demuxConn struct {
	d     *udpDemux
	proto udpProto
	in    chan packet
	done  chan struct{}
	once  sync.Once

	mu        sync.Mutex
	rDeadline time.Time
	notify    chan struct{} // closed + replaced when the read deadline changes
}

func newDemuxConn(d *udpDemux, proto udpProto) *demuxConn {
	return &demuxConn{
		d: d, proto: proto,
		in:     make(chan packet, 256),
		done:   make(chan struct{}),
		notify: make(chan struct{}),
	}
}

func (c *demuxConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		deadline := c.rDeadline
		notify := c.notify
		c.mu.Unlock()

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, nil, errDeadline{}
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case pkt := <-c.in:
			return copy(p, pkt.data), pkt.addr, nil
		case <-timeout:
			return 0, nil, errDeadline{}
		case <-notify:
			// read deadline changed — re-evaluate (this is also how a blocked
			// read is woken when a deadline is set).
		case <-c.done:
			return 0, nil, net.ErrClosed
		}
	}
}

func (c *demuxConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}
	return c.d.conn.WriteTo(p, addr)
}

func (c *demuxConn) close() { c.once.Do(func() { close(c.done) }) }

func (c *demuxConn) Close() error { c.close(); return nil }

func (c *demuxConn) LocalAddr() net.Addr { return c.d.conn.LocalAddr() }

func (c *demuxConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	close(c.notify) // wake any blocked ReadFrom so it picks up the new deadline
	c.notify = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *demuxConn) SetWriteDeadline(t time.Time) error { return c.d.conn.SetWriteDeadline(t) }

func (c *demuxConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t) //nolint:errcheck
	return c.SetWriteDeadline(t)
}

// errDeadline is the net.Error returned when a demuxConn read deadline elapses.
type errDeadline struct{}

func (errDeadline) Error() string   { return "udpdemux: i/o timeout" }
func (errDeadline) Timeout() bool   { return true }
func (errDeadline) Temporary() bool { return true }
