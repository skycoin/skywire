// Package dm pkg/skychat/dm/controller.go c4-app-chat
//
// Controller is the shared 1:1 (direct-message) skychat core: it owns the
// per-peer framed connections, the listen/accept + per-conn read loops, the
// chat-msg / chat-ack / quoted-reply envelope handling, the outbound send path
// (with optional peer-receipt ack-wait and cross-network fallback), and best-
// effort persistence. It is transport-agnostic (a small Client seam satisfied
// by *app.Client on both the native visor and the wasm visor) and surfaces
// every message — inbound and the outbound mirror — through one OnEvent
// callback, so a consumer's UI/SSE layer stays out of the wire mechanics.
//
// This is the extraction of what used to be duplicated: the native app's
// dialAndCache / tryNetworkFallback / handleConn / listenLoop / messageHandler
// send-core / ack-waiter machinery, and the wasm visor's ad-hoc
// conns/readChatConn/sendChat. The wire codec is pkg/skychat/message; the
// persistence backend is pkg/skychat/history (both js-safe), so this package
// compiles under GOOS=js as well as native.
package dm

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// chatPort is skychat's well-known routing port. Peers dial each other here.
const chatPort = routing.Port(1)

// writeTimeout bounds a single WriteFrame so a stalled peer (half-dead
// transport) can't pin the caller indefinitely while holding the conn's write
// mutex — the wedge documented at length in the native app before this moved
// here. Above typical send latency, below the operator ack budget.
const writeTimeout = 5 * time.Second

// Client is the transport seam — the subset of *app.Client the controller
// needs. Both the native and the wasm visor supply an *app.Client.
type Client interface {
	Dial(appnet.Addr) (net.Conn, error)
	Listen(appnet.Type, routing.Port) (net.Listener, error)
}

// Event is one surfaced direct message — an inbound message or the mirror of a
// local send. The consumer maps it into its own UI/SSE shape.
type Event struct {
	ID      string    // message envelope id ("" for a plain, pre-envelope message)
	Dir     string    // "in" | "out"
	Peer    string    // the OTHER end's PK hex (sender for in, recipient for out)
	Network string    // "dmsg" | "skynet" | ...
	Text    string    // message body
	ReplyTo string    // quoted-reply target id ("" if not a reply)
	TS      time.Time // receive (in) or send (out) time, UTC
}

// SendOpts tunes one send.
type SendOpts struct {
	// ReplyTo, when set, quotes the message with this id (threaded reply).
	ReplyTo string
	// WaitAck > 0 requests a peer-receipt ack: the message rides a chat-msg
	// envelope with ack=true and Send blocks up to WaitAck for the peer's
	// chat-ack. Zero = fire-and-forget (default).
	WaitAck time.Duration
	// RequestAck asks the peer for a chat-ack WITHOUT blocking: the message
	// rides an id'd envelope with ack=true exactly like WaitAck, but Send
	// returns as soon as the write lands and the ack surfaces later through
	// OnReceipt. This is the message-status lifecycle a UI wants (sent →
	// received → read) as opposed to a CLI's synchronous --wait. Ignored when
	// WaitAck is set, which already asks for the ack and waits for it.
	RequestAck bool
	// Auto picks the network automatically instead of using the netType arg:
	// reuse a warm conn if one exists (so replies also follow the peer's chosen
	// path), else send over dmsg immediately — no route setup, instant first
	// contact — and warm a skynet route in the background so subsequent messages
	// upgrade to the faster/steadier routed transport. netType is ignored.
	Auto bool
}

// SendResult reports the outcome of a send.
type SendResult struct {
	ID      string        // the envelope id assigned to this message ("" for a plain send)
	Acked   bool          // true if a WaitAck send received its ack in time
	Elapsed time.Duration // time from write to ack (only meaningful when Acked)
	Network appnet.Type   // the network the message actually went out on (may differ via fallback)
}

// Config configures a Controller.
type Config struct {
	// Client is the transport (dial/listen). Required.
	Client Client
	// Store persists messages (nil = ephemeral; delivery is never blocked).
	Store history.Store
	// Networks the controller listens + sends on (e.g. dmsg, skynet).
	Networks []appnet.Type
	// Port the controller listens on / dials. Zero = the well-known chat port (1).
	Port routing.Port
	// OnEvent is called for every surfaced message (inbound + outbound mirror).
	// Never nil in practice; a nil callback simply drops surfacing.
	OnEvent func(Event)
	// OnReceipt is called for an inbound receipt about a message WE sent: a
	// chat-ack (the peer's app received it) or a chat-read (the peer actually
	// displayed it). id is the envelope id of the original message, so a UI can
	// advance that bubble's delivery status; peer is who the receipt came from.
	// Receipts are consumed here — they are never surfaced as chat lines.
	// Nil = the app doesn't track delivery status.
	OnReceipt func(peer, id, kind string)
	// OnDelete is called for an inbound chat-delete: the peer asks us to drop
	// (tombstone) a message THEY sent us, identified by its envelope id. Like a
	// receipt it is consumed here, never surfaced as a chat line; the app
	// replaces the message with a "deleted" placeholder. Nil = deletes ignored.
	OnDelete func(peer, id string)
	// StaleAckWindow bounds how long a RequestAck send waits for the peer's
	// chat-ack before concluding the cached conn is half-open and evicting it,
	// so the next send redials. A live peer acks in well under a second; this
	// is generous so a momentarily-slow peer doesn't cost a healthy conn.
	// Zero disables the check.
	StaleAckWindow time.Duration
	// DialRetry, if set, wraps each dial attempt (e.g. a netutil.Retrier's Do)
	// to preserve the native app's retry behavior. Nil = single attempt.
	DialRetry func(context.Context, func() error) error
	// PreHandleFrame, if set, is offered every inbound frame before chat
	// decoding; returning true consumes the frame (used by the native app for
	// its pair-control envelopes). Nil = no interception.
	PreHandleFrame func(peer cipher.PubKey, payload []byte) bool
	// AlwaysID makes every send ride a chat-msg envelope with a minted id, even
	// a plain (no-reply, no-ack) message — so every message is addressable for
	// replies. The wasm visor sets this (its whole buffer is id-keyed). The
	// native app leaves it false so a default send stays byte-identical on the
	// wire for pre-envelope peers.
	AlwaysID bool
	// Log is an optional structured logger sink.
	Log func(format string, args ...interface{})
}

// Controller is the shared DM core. Create with New; bring listeners up with
// Start; send with Send; tear down with Close.
type Controller struct {
	cfg  Config
	port routing.Port

	mu    sync.Mutex
	conns map[cipher.PubKey]*message.Conn

	// warming guards the auto-mode background skynet upgrade so at most one
	// warm-dial runs per peer at a time.
	warmMu  sync.Mutex
	warming map[cipher.PubKey]bool

	ackMu      sync.Mutex
	ackWaiters map[string]chan struct{}

	statsMu sync.Mutex
	stats   Stats

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// Stats is a snapshot of the controller's message counters, for a status probe.
type Stats struct {
	InboundMsgs    int64
	OutboundMsgs   int64
	InboundDrops   int64 // conn read errors (peer/transport dropped)
	OutboundFails  int64 // sends that failed even after retry + fallback
	OutboundRetry  int64 // stale-cached-conn writes rescued by a redial+retry
	OutboundFallbk int64 // sends rescued by the alternate network
	LastRxAt       time.Time
	LastTxAt       time.Time
}

// Stats returns a snapshot of the message counters.
func (c *Controller) Stats() Stats {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	return c.stats
}

func (c *Controller) bump(f func(*Stats)) {
	c.statsMu.Lock()
	f(&c.stats)
	c.statsMu.Unlock()
}

// New builds a Controller. It does not open any connection until Start.
func New(cfg Config) *Controller {
	port := cfg.Port
	if port == 0 {
		port = chatPort
	}
	return &Controller{
		cfg:        cfg,
		port:       port,
		conns:      make(map[cipher.PubKey]*message.Conn),
		warming:    make(map[cipher.PubKey]bool),
		ackWaiters: make(map[string]chan struct{}),
		done:       make(chan struct{}),
	}
}

func (c *Controller) log(format string, args ...interface{}) {
	if c.cfg.Log != nil {
		c.cfg.Log(format, args...)
	}
}

// Start opens a listener on each configured network and accepts inbound
// connections until ctx is canceled or Close is called. Returns an error only
// if NO listener could be started (a per-network failure is logged and skipped,
// matching the native + wasm behavior of keeping whatever listeners came up).
func (c *Controller) Start(ctx context.Context) error {
	started := 0
	for _, n := range c.cfg.Networks {
		lis, err := c.cfg.Client.Listen(n, c.port)
		if err != nil {
			c.log("skychat/dm: listen %s:%d: %v", n, c.port, err)
			continue
		}
		started++
		c.log("skychat/dm: listening on %s:%d", n, c.port)
		c.wg.Add(1)
		go func(l net.Listener) {
			defer c.wg.Done()
			<-doneOrCtx(ctx, c.done)
			_ = l.Close() //nolint:errcheck
		}(lis)
		c.wg.Add(1)
		go func(l net.Listener) {
			defer c.wg.Done()
			c.acceptLoop(l)
		}(lis)
	}
	if started == 0 {
		return errors.New("skychat/dm: no listeners started")
	}
	return nil
}

// acceptLoop runs every inbound conn through Serve, which caches it before
// reading.
//
// The caching is the point. An accepted conn is a live connection to that peer
// exactly as much as a dialed one is, but leaving it out of the cache made
// HasConn mean "a peer we have dialed" rather than "a peer we are connected
// to" — and the file-transfer accept policy reads HasConn as the latter (see
// isEstablishedPeer in the skychat app). A peer who had only ever connected TO
// us therefore had every file offer declined until we happened to dial them
// back, while their text messages went through the whole time, since those
// arrive on this very conn.
func (c *Controller) acceptLoop(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.Serve(conn)
		}()
	}
}

// Serve runs a conn (whose RemoteAddr() reports an appnet.Addr) through the
// cache + read loop. It is the accept path above, and also the entry point for
// transports the Client seam doesn't cover — the native app's TCP-direct
// handshake hands its conn here. Blocks until the conn closes (the caller
// decides whether to run it in a goroutine).
func (c *Controller) Serve(conn net.Conn) {
	fc := message.NewConn(conn)
	if raddr, ok := conn.RemoteAddr().(appnet.Addr); ok {
		c.mu.Lock()
		c.conns[raddr.PubKey] = fc
		c.mu.Unlock()
	}
	c.readConn(fc)
}

// dialAndCache dials pk at addr (through DialRetry if configured), wraps the
// conn in framing, caches it by PK, and starts its read loop. The read loop's
// lifetime is the peer connection, not any request.
func (c *Controller) dialAndCache(ctx context.Context, pk cipher.PubKey, addr appnet.Addr) (*message.Conn, error) {
	if c.cfg.Client == nil {
		return nil, errors.New("skychat/dm: no transport configured (dial unavailable)")
	}
	var raw net.Conn
	dial := func() error {
		cc, err := c.cfg.Client.Dial(addr)
		if err != nil {
			return err
		}
		raw = cc
		return nil
	}
	var err error
	if c.cfg.DialRetry != nil {
		err = c.cfg.DialRetry(ctx, dial)
	} else {
		err = dial()
	}
	if err != nil {
		return nil, err
	}
	fc := message.NewConn(raw)
	c.mu.Lock()
	c.conns[pk] = fc
	c.mu.Unlock()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.readConn(fc)
	}()
	return fc, nil
}

// tryNetworkFallback dials the OTHER configured network once and caches it.
// Returns (nil, _) when there's no alternate enabled network or the dial fails.
func (c *Controller) tryNetworkFallback(ctx context.Context, pk cipher.PubKey, current appnet.Type) (*message.Conn, appnet.Type) {
	var alt appnet.Type
	switch current {
	case appnet.TypeSkynet:
		alt = appnet.TypeDmsg
	case appnet.TypeDmsg:
		alt = appnet.TypeSkynet
	default:
		return nil, ""
	}
	if !c.hasNetwork(alt) {
		return nil, ""
	}
	fc, err := c.dialAndCache(ctx, pk, appnet.Addr{Net: alt, PubKey: pk, Port: c.port})
	if err != nil {
		c.log("skychat/dm: fallback dial %s→%s failed: %v", current, alt, err)
		return nil, alt
	}
	return fc, alt
}

func (c *Controller) hasNetwork(n appnet.Type) bool {
	for _, x := range c.cfg.Networks {
		if x == n {
			return true
		}
	}
	return false
}

// warmDialTimeout bounds the auto-mode background skynet dial (route setup can
// take a few seconds; give up rather than hang a warm-goroutine indefinitely).
const warmDialTimeout = 25 * time.Second

// warmNetwork dials net→pk in the background (auto mode) and caches the conn on
// success, so the next auto send upgrades from dmsg to the routed transport. A
// per-peer guard keeps concurrent sends from stacking dials; on failure the
// existing dmsg conn stays cached and the next send simply retries the warm.
func (c *Controller) warmNetwork(pk cipher.PubKey, net appnet.Type) {
	if c.cfg.Client == nil || !c.hasNetwork(net) {
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	c.warmMu.Lock()
	if c.warming[pk] {
		c.warmMu.Unlock()
		return
	}
	c.warming[pk] = true
	c.warmMu.Unlock()
	defer func() {
		c.warmMu.Lock()
		delete(c.warming, pk)
		c.warmMu.Unlock()
	}()

	// Already on the target network? nothing to do.
	c.mu.Lock()
	old := c.conns[pk]
	c.mu.Unlock()
	if old != nil {
		if ra, ok := old.RemoteAddr().(appnet.Addr); ok && ra.Net == net {
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), warmDialTimeout)
	defer cancel()
	newConn, err := c.dialAndCache(ctx, pk, appnet.Addr{Net: net, PubKey: pk, Port: c.port})
	if err != nil {
		c.log("skychat/dm: auto warm %s dial to %s failed: %v", net, short(pk.Hex()), err)
		return
	}
	// dialAndCache replaced c.conns[pk] with the upgraded conn. Close the
	// superseded one so its read goroutine exits (a live one would block Close)
	// and we don't keep a duplicate conn to the peer; a send racing on the old
	// conn just fails its write and redials, which is already handled.
	if old != nil && old != newConn {
		_ = old.Close() //nolint
	}
}

// readConn reads framed messages off one peer conn until it errors/closes.
// Handles the pre-handler hook (pairing), the chat-msg/chat-ack/reply envelope
// (sending a chat-ack back when requested), persistence, and surfacing.
func (c *Controller) readConn(conn *message.Conn) {
	raddr, _ := conn.RemoteAddr().(appnet.Addr)
	peer := raddr.PubKey
	defer func() {
		c.mu.Lock()
		if c.conns[peer] == conn {
			delete(c.conns, peer)
		}
		c.mu.Unlock()
		_ = conn.Close() //nolint:errcheck
	}()
	for {
		payload, err := conn.ReadFrame()
		if err != nil {
			c.bump(func(s *Stats) { s.InboundDrops++ })
			return
		}
		if c.cfg.PreHandleFrame != nil && c.cfg.PreHandleFrame(peer, payload) {
			continue
		}
		// A receipt (chat-ack / chat-read) is about a message WE sent: report
		// it and let decodeInbound consume it. Checked before decoding so the
		// app sees acks too, not only reads.
		if kind, id := receiptKind(payload); kind != "" && c.cfg.OnReceipt != nil {
			c.cfg.OnReceipt(peer.Hex(), id, kind)
		}
		// A chat-delete asks us to tombstone a message the peer sent us. Reported
		// then consumed (decodeInbound returns empty for it, so it never surfaces
		// as a chat line).
		if id := deleteID(payload); id != "" && c.cfg.OnDelete != nil {
			c.cfg.OnDelete(peer.Hex(), id)
		}
		text, ackID, replyTo, msgID := c.decodeInbound(payload)
		if ackID != "" {
			if b, mErr := (message.Envelope{Type: message.TypeAck, ID: ackID}).Marshal(); mErr == nil {
				if wErr := conn.WriteFrame(b); wErr != nil {
					c.log("skychat/dm: ack to %s failed: %v", peer.Hex(), wErr)
				}
			}
		}
		if text == "" {
			continue // ack-only / empty — nothing to surface
		}
		peerHex := peer.Hex()
		c.bump(func(s *Stats) { s.InboundMsgs++; s.LastRxAt = time.Now().UTC() })
		c.persist(history.Message{
			Peer: peerHex, From: peerHex, Outgoing: false,
			Text: text, Timestamp: time.Now().UTC(), ID: msgID, ReplyTo: replyTo,
		})
		c.emit(Event{
			ID: msgID, Dir: "in", Peer: peerHex, Network: string(raddr.Net),
			Text: text, ReplyTo: replyTo, TS: time.Now().UTC(),
		})
	}
}

// Send delivers text to pk over net. It mirrors the native send path: build the
// wire payload (plain, or a chat-msg envelope when a reply or ack is needed),
// (re)use a cached conn, write with a deadline, redial+retry once on a stale
// cached-conn write, fall back to the alternate network once, persist, surface
// the outbound mirror, and — for a WaitAck send — block on the peer's chat-ack.
func (c *Controller) Send(ctx context.Context, pk cipher.PubKey, netType appnet.Type, text string, opt SendOpts) (SendResult, error) {
	if text == "" {
		return SendResult{}, errors.New("skychat/dm: empty message")
	}
	var res SendResult
	res.Network = netType

	// Auto: pick the network. Reuse a warm conn if one exists (which also makes
	// this side's replies follow the path the peer established); otherwise send
	// over dmsg now and warm skynet in the background for the next message.
	autoWarm := false
	if opt.Auto {
		c.mu.Lock()
		cached := c.conns[pk]
		c.mu.Unlock()
		switch {
		case cached != nil:
			if ra, ok := cached.RemoteAddr().(appnet.Addr); ok && ra.Net != "" {
				netType = ra.Net
			}
		case c.hasNetwork(appnet.TypeDmsg):
			netType = appnet.TypeDmsg
			autoWarm = c.hasNetwork(appnet.TypeSkynet)
		default:
			netType = appnet.TypeSkynet // dmsg not enabled; skynet is all we have
		}
		res.Network = netType
	}

	wire := []byte(text)
	// Both ack modes put ack=true on the wire; they differ only in whether this
	// call waits for the answer. WaitAck blocks here, RequestAck lets it arrive
	// later through OnReceipt (and backs the stale-conn check below).
	wantAck := opt.WaitAck > 0 || opt.RequestAck
	var ackID string
	var ackCh <-chan struct{}
	var unreg func()
	if wantAck || opt.ReplyTo != "" || c.cfg.AlwaysID {
		ackID = newID()
		res.ID = ackID
		env := message.Envelope{Type: message.TypeMsg, ID: ackID, Body: text, Ack: wantAck, ReplyTo: opt.ReplyTo}
		b, err := env.Marshal()
		if err != nil {
			return res, fmt.Errorf("skychat/dm: encode: %w", err)
		}
		wire = b
		if opt.WaitAck > 0 || (opt.RequestAck && c.cfg.StaleAckWindow > 0) {
			// Registered BEFORE the write so a fast ack can't be missed. The
			// deferred cleanup below releases it on every path except the
			// RequestAck hand-off, where the watcher goroutine takes ownership.
			ackCh, unreg = c.registerAck(ackID)
		}
	}
	handedOff := false
	defer func() {
		if unreg != nil && !handedOff {
			unreg()
		}
	}()

	addr := appnet.Addr{Net: netType, PubKey: pk, Port: c.port}
	c.mu.Lock()
	conn := c.conns[pk]
	c.mu.Unlock()
	cached := conn != nil
	if !cached {
		var err error
		conn, err = c.dialAndCache(ctx, pk, addr)
		if err != nil {
			return res, fmt.Errorf("dial %s over %s: %w", short(pk.Hex()), netType, err)
		}
	}

	writeStart := time.Now()
	err := conn.WriteFrameDeadline(wire, writeTimeout)
	if err != nil && cached {
		// Stale cached-conn write: evict (pointer-guarded), redial, retry once.
		c.evict(pk, conn)
		if newConn, derr := c.dialAndCache(ctx, pk, addr); derr == nil {
			if werr := newConn.WriteFrameDeadline(wire, writeTimeout); werr == nil {
				conn, err = newConn, nil
				c.bump(func(s *Stats) { s.OutboundRetry++ })
			} else {
				c.evict(pk, newConn)
				err = werr
			}
		} else {
			err = fmt.Errorf("redial after write %v: %w", err, derr)
		}
	}
	if err != nil {
		c.evict(pk, conn)
		// Last resort: the alternate network.
		if fbConn, alt := c.tryNetworkFallback(ctx, pk, netType); fbConn != nil {
			if werr := fbConn.WriteFrameDeadline(wire, writeTimeout); werr == nil {
				res.Network, err = alt, nil
				c.bump(func(s *Stats) { s.OutboundFallbk++ })
			} else {
				c.evict(pk, fbConn)
			}
		}
		if err != nil {
			c.bump(func(s *Stats) { s.OutboundFails++ })
			return res, err
		}
	}

	c.bump(func(s *Stats) { s.OutboundMsgs++; s.LastTxAt = time.Now().UTC() })

	// auto mode: the message went out over dmsg (fast first contact); warm a
	// skynet route in the background so the next message upgrades to it.
	if autoWarm && res.Network == appnet.TypeDmsg {
		go c.warmNetwork(pk, appnet.TypeSkynet) //nolint
	}

	c.persist(history.Message{
		Peer: pk.Hex(), Outgoing: true, Text: text,
		Timestamp: time.Now().UTC(), ID: ackID, ReplyTo: opt.ReplyTo,
	})
	c.emit(Event{
		ID: ackID, Dir: "out", Peer: pk.Hex(), Network: string(res.Network),
		Text: text, ReplyTo: opt.ReplyTo, TS: time.Now().UTC(),
	})

	switch {
	case opt.WaitAck > 0 && ackCh != nil:
		timer := time.NewTimer(opt.WaitAck)
		defer timer.Stop()
		select {
		case <-ackCh:
			res.Acked = true
			res.Elapsed = time.Since(writeStart)
		case <-timer.C:
		case <-ctx.Done():
			return res, ctx.Err()
		}
	case opt.RequestAck && ackCh != nil:
		// Don't wait — but do watch. A half-open cached conn (peer restarted,
		// idle-dead transport) accepts the write without error while the frame
		// never lands, so no ack comes back; evicting it means the next send
		// redials instead of writing into the void again.
		handedOff = true
		go c.watchAck(pk, conn, ackCh, unreg, c.cfg.StaleAckWindow)
	}
	return res, nil
}

// watchAck backs RequestAck's stale-conn recovery: it waits for the peer's
// chat-ack and, if none arrives within window, evicts the conn the message was
// written to. A live peer acks in well under a second, so on a healthy conn
// this returns via ackCh and leaves the cache untouched.
func (c *Controller) watchAck(pk cipher.PubKey, conn *message.Conn, ackCh <-chan struct{}, unreg func(), window time.Duration) {
	if unreg != nil {
		defer unreg()
	}
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-ackCh:
		// Acked — the conn is alive, keep it cached.
	case <-timer.C:
		c.evict(pk, conn)
		c.log("skychat/dm: no receipt from %s within %v — dropped stale conn; next send will redial", short(pk.Hex()), window)
	case <-c.done:
		// Controller shutting down.
	}
}

// SendRaw writes a pre-framed raw payload to pk over a cached or freshly-dialed
// conn, trying each configured network in order. No envelope, persistence, or
// surfacing — it's for out-of-band control frames (the native app's
// pair-invite / pair-ack) whose inbound side is consumed by PreHandleFrame.
func (c *Controller) SendRaw(ctx context.Context, pk cipher.PubKey, payload []byte) error {
	c.mu.Lock()
	conn := c.conns[pk]
	c.mu.Unlock()
	if conn != nil {
		if err := conn.WriteFrameDeadline(payload, writeTimeout); err == nil {
			return nil
		}
		c.evict(pk, conn)
	}
	var lastErr error
	for _, n := range c.cfg.Networks {
		fc, err := c.dialAndCache(ctx, pk, appnet.Addr{Net: n, PubKey: pk, Port: c.port})
		if err != nil {
			lastErr = err
			continue
		}
		if werr := fc.WriteFrameDeadline(payload, writeTimeout); werr == nil {
			return nil
		} else { //nolint:revive
			c.evict(pk, fc)
			lastErr = werr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("skychat/dm: no network to send raw frame")
	}
	return lastErr
}

// SendDelete sends a delete-for-everyone command for the message id to pk over a
// cached-or-dialed conn. On the peer, OnDelete fires and the message is
// tombstoned. Best-effort like a receipt — a peer that's offline simply never
// applies it (the sender's own copy is deleted locally by the caller).
func (c *Controller) SendDelete(ctx context.Context, pk cipher.PubKey, id string) error {
	b, err := (message.Envelope{Type: message.TypeDelete, ID: id}).Marshal()
	if err != nil {
		return err
	}
	return c.SendRaw(ctx, pk, b)
}

// SendRead sends a read receipt for the message id to pk (the message's original
// sender), over a cached-or-dialed conn. On the sender, OnReceipt fires with kind
// "read" so their bubble's tick advances to read. Best-effort like SendDelete —
// a peer that's offline simply never advances the tick.
func (c *Controller) SendRead(ctx context.Context, pk cipher.PubKey, id string) error {
	b, err := (message.Envelope{Type: message.TypeRead, ID: id}).Marshal()
	if err != nil {
		return err
	}
	return c.SendRaw(ctx, pk, b)
}

func (c *Controller) evict(pk cipher.PubKey, conn *message.Conn) {
	c.mu.Lock()
	if c.conns[pk] == conn {
		delete(c.conns, pk)
	}
	c.mu.Unlock()
}

// Conns returns the number of cached peer connections (for status probes).
func (c *Controller) Conns() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.conns)
}

// HasConn reports whether a live cached connection to pk exists.
func (c *Controller) HasConn(pk cipher.PubKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.conns[pk]
	return ok
}

// NoteInbound records an inbound message that arrived outside the framed-conn
// read loop (e.g. the native app's CXO feed path) so the /status counters stay
// accurate for those transports too.
func (c *Controller) NoteInbound() {
	c.bump(func(s *Stats) { s.InboundMsgs++; s.LastRxAt = time.Now().UTC() })
}

// Peers returns the PK hexes of every cached peer connection (for /status).
func (c *Controller) Peers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.conns))
	for pk := range c.conns {
		out = append(out, pk.Hex())
	}
	return out
}

// Close stops accept/read loops and closes every cached conn. Idempotent.
func (c *Controller) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		for pk, conn := range c.conns {
			_ = conn.Close() //nolint:errcheck
			delete(c.conns, pk)
		}
		c.mu.Unlock()
	})
	return nil
}

func (c *Controller) persist(m history.Message) {
	if c.cfg.Store == nil {
		return
	}
	if err := c.cfg.Store.Append(m); err != nil {
		// Rate-limit / cap / whitelist rejections are expected and non-fatal:
		// the message is still surfaced, only durable storage is skipped.
		c.log("skychat/dm: history append %s: %v", m.Peer, err)
	}
}

func (c *Controller) emit(ev Event) {
	if c.cfg.OnEvent != nil {
		c.cfg.OnEvent(ev)
	}
}

// --- ack routing ------------------------------------------------------------

func (c *Controller) registerAck(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	c.ackMu.Lock()
	c.ackWaiters[id] = ch
	c.ackMu.Unlock()
	return ch, func() {
		c.ackMu.Lock()
		delete(c.ackWaiters, id)
		c.ackMu.Unlock()
	}
}

func (c *Controller) deliverAck(id string) {
	c.ackMu.Lock()
	ch, ok := c.ackWaiters[id]
	c.ackMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// --- helpers ----------------------------------------------------------------

// decodePayload extracts (text, ackID, replyTo, msgID) from a frame. For a
// chat-msg envelope it returns the body, the id to ack (non-empty only when the
// peer requested one), the reply target, and the message's own id. For a
// chat-ack it consumes the ack (routing it to any waiter) and returns empty
// text. A plain (non-envelope) frame returns the raw bytes as text.
func (c *Controller) decodeInbound(payload []byte) (text, ackID, replyTo, msgID string) {
	if env, ok := message.ParseEnvelope(payload); ok {
		switch env.Type {
		case message.TypeAck:
			if env.ID != "" {
				c.deliverAck(env.ID)
			}
			return "", "", "", ""
		case message.TypeRead:
			// A read receipt for a message WE sent. Consumed like an ack —
			// nothing to surface — but reported so the sender's UI can advance
			// that bubble from "received" to "read". Handled here rather than
			// falling through, or the raw JSON would show up as a chat line.
			return "", "", "", ""
		case message.TypeDelete:
			// A delete-for-everyone command. Consumed here (reported via OnDelete
			// in readConn); never surfaced as a chat line.
			return "", "", "", ""
		case message.TypeMsg:
			ack := ""
			if env.Ack && env.ID != "" {
				ack = env.ID
			}
			return env.Body, ack, env.ReplyTo, env.ID
		}
	}
	return string(payload), "", "", ""
}

// receiptKind reports the receipt type a frame carries about a message we sent
// (message.TypeAck / message.TypeRead) plus the id it refers to, or "" for
// anything that is not a receipt.
func receiptKind(payload []byte) (kind, id string) {
	env, ok := message.ParseEnvelope(payload)
	if !ok || env.ID == "" {
		return "", ""
	}
	switch env.Type {
	case message.TypeAck, message.TypeRead:
		return env.Type, env.ID
	}
	return "", ""
}

// deleteID reports the target id of an inbound chat-delete frame, or "".
func deleteID(payload []byte) string {
	env, ok := message.ParseEnvelope(payload)
	if !ok || env.Type != message.TypeDelete || env.ID == "" {
		return ""
	}
	return env.ID
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
}

func short(pk string) string {
	if len(pk) > 8 {
		return pk[:8]
	}
	return pk
}

// doneOrCtx returns a channel that fires when either ctx is done or done closes.
func doneOrCtx(ctx context.Context, done chan struct{}) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		close(out)
	}()
	return out
}
