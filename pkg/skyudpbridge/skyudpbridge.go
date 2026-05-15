// Package skyudpbridge is the protocol core for the skywire UDP
// bridge ("sub"): length-prefixed UDP datagrams ferried over a
// reliable skywire byte-stream (dmsg or skywire-routing). Plan B
// of the UDP-over-skynet split (Plan A — true packet-level UDP at
// the route-group layer — lives in pkg/router under RFC #2607).
//
// Use shape: a co-located application speaks UDP to a local socket
// (e.g. DNS resolver hitting :53). The client-side bridge captures
// each datagram, frames it with a uint16 length prefix, and writes
// it to a skywire stream. The peer's server-side bridge reads
// frames off the stream and replays them as UDP to a configured
// local target (e.g. an upstream resolver on 127.0.0.1:53). The
// stream stays open for the lifetime of the UDP "flow" (per-source
// (ip, port) tuple) with an idle timeout.
//
// Two ends, mirror-symmetric:
//
//	client side:  UDP listener  →  per-source stream  →  framer  →  Stream
//	server side:  Stream  →  defamer  →  per-stream UDP socket  →  target
//
// Replies flow back via the SAME stream/UDP socket pair, so a
// stateful UDP protocol (DNS reply on same socket) works.
//
// Suitable for non-realtime UDP: DNS, NTP, SNMP, MQTT-SN, WireGuard
// control plane. For media-class UDP (VoIP, RTP, WebRTC media,
// real-time game protocols) use Plan A (true packet-level UDP at
// the route-group layer; see RFC #2607).
//
// Reliability tradeoff: the skywire stream is TCP-like (reliable,
// in-order). Wrapping UDP in it gains reliability + ordering but
// adds head-of-line blocking. For DNS/NTP/SNMP this is fine — the
// app would retry on packet loss anyway, and replies arriving in
// order is harmless. For RTP/voice it's wrong — Plan A.
//
// The Dialer abstraction lets the standalone (cmd/sub) and any
// future visor-app variant share this protocol core: standalone
// backs Dialer with its own dmsg.Client; visor-app would back it
// with app.Client.Dial. Same pattern as pkg/skymailbridge.
package skyudpbridge

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
)

// MaxFrameSize is the largest UDP payload the bridge will ferry in
// a single frame. Set to 65535 (the absolute UDP payload limit) so
// no realistic datagram is truncated. Frames larger than this are
// dropped on the client side and never reach the wire.
const MaxFrameSize = 65535

// DefaultIdleTimeout is how long a per-source flow's stream stays
// open with no traffic before the bridge closes it. Trades stream
// reuse (cheaper for chatty flows) against resource bloat (a stuck
// flow consuming a stream forever). 60s is the same default WireGuard
// uses for keepalive-driven NAT entries — a sane neighborhood.
const DefaultIdleTimeout = 60 * time.Second

// DefaultDialTimeout bounds a single peer-dial attempt. Long enough
// to ride out a skywire route setup, short enough that a UDP app
// gets a "we tried" failure rather than indefinite blocking.
const DefaultDialTimeout = 15 * time.Second

// Dialer abstracts the peer-dial step so the same protocol core
// runs on top of either a direct dmsg.Client (standalone) or
// app.Client.Dial (visor app). Implementations must return a
// usable net.Conn or an error; returning (nil, nil) is a contract
// violation. Mirrors pkg/skymailbridge.Dialer by intent.
type Dialer interface {
	Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error)
}

// ClientConfig configures a client-side bridge instance.
type ClientConfig struct {
	// ListenUDP is the local UDP address the bridge listens on for
	// app traffic. Conventionally "127.0.0.1:<port>". Required.
	ListenUDP string
	// Peer is the destination visor's public key. Required.
	Peer cipher.PubKey
	// PeerPort is the skywire routing/dmsg port the peer's
	// server-side bridge listens on. Required (no default —
	// operators must agree on a port).
	PeerPort uint16
	// IdleTimeout is the per-flow idle timer. Zero uses
	// DefaultIdleTimeout.
	IdleTimeout time.Duration
	// DialTimeout caps each peer-dial attempt. Zero uses
	// DefaultDialTimeout.
	DialTimeout time.Duration
}

// ServerConfig configures a server-side bridge instance.
type ServerConfig struct {
	// TargetUDP is the local UDP address the server forwards
	// unframed datagrams to. Conventionally "127.0.0.1:<port>".
	// Required.
	TargetUDP string
	// IdleTimeout is the per-stream UDP-socket idle timer.
	IdleTimeout time.Duration
}

// RunClient starts the client-side bridge: UDP listener →
// per-source stream → framer. Blocks until ctx is canceled or the
// underlying listener fails. The Dialer is invoked lazily on the
// first datagram from each new source.
func RunClient(ctx context.Context, cfg ClientConfig, dialer Dialer, log logrus.FieldLogger) error {
	if dialer == nil {
		return errors.New("skyudpbridge: Dialer is nil")
	}
	if cfg.ListenUDP == "" {
		return errors.New("skyudpbridge: ListenUDP is required")
	}
	if cfg.PeerPort == 0 {
		return errors.New("skyudpbridge: PeerPort is required")
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	if log == nil {
		log = logrus.NewEntry(logrus.New())
	}

	udpAddr, err := net.ResolveUDPAddr("udp", cfg.ListenUDP)
	if err != nil {
		return fmt.Errorf("resolve listen udp %q: %w", cfg.ListenUDP, err)
	}
	udpC, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp %q: %w", cfg.ListenUDP, err)
	}
	defer udpC.Close() //nolint:errcheck,gosec

	c := &clientState{
		cfg:    cfg,
		dialer: dialer,
		log:    log,
		udpC:   udpC,
		flows:  make(map[string]*clientFlow),
	}
	go func() {
		<-ctx.Done()
		_ = udpC.Close() //nolint:errcheck,gosec
		// Close active flows too — serveUDP may be blocked in
		// WriteFrame on a stream whose peer stopped reading,
		// and closing the udpC alone wouldn't unblock it.
		c.closeAllFlows()
	}()

	err = c.serveUDP(ctx)
	c.closeAllFlows()
	return err
}

// RunServer starts the server-side bridge: accept skywire streams,
// read framed datagrams, forward via UDP. Blocks until ctx is
// canceled or the listener fails. accept is the inbound-stream
// accept function — provided by the caller because dmsg and
// skywire-routing have different listen primitives.
func RunServer(ctx context.Context, cfg ServerConfig, accept func() (net.Conn, error), log logrus.FieldLogger) error {
	if accept == nil {
		return errors.New("skyudpbridge: accept fn is nil")
	}
	if cfg.TargetUDP == "" {
		return errors.New("skyudpbridge: TargetUDP is required")
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if log == nil {
		log = logrus.NewEntry(logrus.New())
	}

	target, err := net.ResolveUDPAddr("udp", cfg.TargetUDP)
	if err != nil {
		return fmt.Errorf("resolve target udp %q: %w", cfg.TargetUDP, err)
	}

	for {
		conn, err := accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go handleServerStream(ctx, conn, target, cfg.IdleTimeout, log)
	}
}

// WriteFrame writes one length-prefixed datagram to w. Returns an
// error if the payload is over MaxFrameSize or the write partially
// completes. Exported so tests + the future visor-app variant share
// the same wire format guarantees.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("skyudpbridge: frame %d > max %d", len(payload), MaxFrameSize)
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload))) //nolint:gosec // bounded above
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed datagram from r into buf.
// Returns the number of bytes read into buf. If the incoming frame
// is larger than len(buf), returns an error (caller should size
// buf to MaxFrameSize for guaranteed acceptance).
func ReadFrame(r io.Reader, buf []byte) (int, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 {
		return 0, nil
	}
	if n > len(buf) {
		return 0, fmt.Errorf("skyudpbridge: frame %d > buf %d", n, len(buf))
	}
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return 0, fmt.Errorf("read frame body: %w", err)
	}
	return n, nil
}

// --- client side internals ---

type clientFlow struct {
	conn     net.Conn
	mu       sync.Mutex
	idleStop chan struct{}
	addr     *net.UDPAddr
}

type clientState struct {
	cfg    ClientConfig
	dialer Dialer
	log    logrus.FieldLogger
	udpC   *net.UDPConn

	mu    sync.Mutex
	flows map[string]*clientFlow // keyed by source addr.String()
}

func (c *clientState) serveUDP(ctx context.Context) error {
	buf := make([]byte, MaxFrameSize)
	for {
		n, src, err := c.udpC.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("udp read: %w", err)
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])

		flow, err := c.getOrDialFlow(ctx, src)
		if err != nil {
			c.log.WithError(err).WithField("src", src.String()).
				Debug("skyudpbridge: dial flow failed; dropping datagram")
			continue
		}
		flow.mu.Lock()
		writeErr := WriteFrame(flow.conn, payload)
		flow.mu.Unlock()
		if writeErr != nil {
			c.log.WithError(writeErr).WithField("src", src.String()).
				Debug("skyudpbridge: frame write failed; tearing down flow")
			c.removeFlow(src)
		}
	}
}

func (c *clientState) getOrDialFlow(ctx context.Context, src *net.UDPAddr) (*clientFlow, error) {
	key := src.String()
	c.mu.Lock()
	if f, ok := c.flows[key]; ok {
		c.mu.Unlock()
		return f, nil
	}
	c.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()
	conn, err := c.dialer.Dial(dialCtx, c.cfg.Peer, c.cfg.PeerPort)
	if err != nil {
		return nil, err
	}
	flow := &clientFlow{
		conn:     conn,
		idleStop: make(chan struct{}),
		addr:     src,
	}

	c.mu.Lock()
	// Double-check in case a concurrent datagram from the same source
	// raced us. If so, close the redundant conn and reuse the existing.
	if existing, ok := c.flows[key]; ok {
		c.mu.Unlock()
		_ = conn.Close() //nolint:errcheck,gosec
		return existing, nil
	}
	c.flows[key] = flow
	c.mu.Unlock()

	// Stream→UDP reply pump.
	go c.pumpReplies(flow, src)
	// Idle reaper.
	go c.idleReaper(flow, src)
	return flow, nil
}

func (c *clientState) pumpReplies(flow *clientFlow, src *net.UDPAddr) {
	buf := make([]byte, MaxFrameSize)
	for {
		n, err := ReadFrame(flow.conn, buf)
		if err != nil {
			c.log.WithError(err).WithField("src", src.String()).
				Debug("skyudpbridge: reply pump stopped")
			c.removeFlow(src)
			return
		}
		if n == 0 {
			continue
		}
		if _, werr := c.udpC.WriteToUDP(buf[:n], src); werr != nil {
			c.log.WithError(werr).WithField("src", src.String()).
				Debug("skyudpbridge: udp reply write failed")
			c.removeFlow(src)
			return
		}
	}
}

func (c *clientState) idleReaper(flow *clientFlow, src *net.UDPAddr) {
	t := time.NewTimer(c.cfg.IdleTimeout)
	defer t.Stop()
	select {
	case <-t.C:
		c.log.WithField("src", src.String()).Debug("skyudpbridge: idle timeout, closing flow")
		c.removeFlow(src)
	case <-flow.idleStop:
		return
	}
}

func (c *clientState) closeAllFlows() {
	c.mu.Lock()
	flows := c.flows
	c.flows = make(map[string]*clientFlow)
	c.mu.Unlock()
	for _, f := range flows {
		close(f.idleStop)
		_ = f.conn.Close() //nolint:errcheck,gosec
	}
}

func (c *clientState) removeFlow(src *net.UDPAddr) {
	key := src.String()
	c.mu.Lock()
	flow, ok := c.flows[key]
	if ok {
		delete(c.flows, key)
	}
	c.mu.Unlock()
	if ok {
		close(flow.idleStop)
		_ = flow.conn.Close() //nolint:errcheck,gosec
	}
}

// --- server side internals ---

func handleServerStream(ctx context.Context, stream net.Conn, target *net.UDPAddr, idleTimeout time.Duration, log logrus.FieldLogger) {
	defer stream.Close() //nolint:errcheck,gosec

	// Per-stream UDP socket. Source-port replies from target land
	// on this socket and route back over this stream — keeping a
	// stateful UDP protocol (DNS req/reply on same socket) working.
	udp, err := net.DialUDP("udp", nil, target)
	if err != nil {
		log.WithError(err).Debug("skyudpbridge: server dial target udp failed")
		return
	}
	defer udp.Close() //nolint:errcheck,gosec

	idleCh := make(chan struct{}, 1)
	// stream→UDP
	go func() {
		buf := make([]byte, MaxFrameSize)
		for {
			n, err := ReadFrame(stream, buf)
			if err != nil {
				select {
				case idleCh <- struct{}{}:
				default:
				}
				return
			}
			if n == 0 {
				continue
			}
			if _, werr := udp.Write(buf[:n]); werr != nil {
				log.WithError(werr).Debug("skyudpbridge: server udp write failed")
				select {
				case idleCh <- struct{}{}:
				default:
				}
				return
			}
		}
	}()
	// UDP→stream
	go func() {
		buf := make([]byte, MaxFrameSize)
		for {
			_ = udp.SetReadDeadline(time.Now().Add(idleTimeout)) //nolint:errcheck,gosec
			n, _, err := udp.ReadFromUDP(buf)
			if err != nil {
				select {
				case idleCh <- struct{}{}:
				default:
				}
				return
			}
			if err := WriteFrame(stream, buf[:n]); err != nil {
				log.WithError(err).Debug("skyudpbridge: server stream write failed")
				select {
				case idleCh <- struct{}{}:
				default:
				}
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
	case <-idleCh:
	}
}
