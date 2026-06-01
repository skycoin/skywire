// Package commands cmd/apps/skychat/skychat.go
package commands

import (
	"context"
	cryptoRand "crypto/rand"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor"
)

var r = netutil.NewRetrier(nil, 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

// Wire-protocol constants for skychat's peer-to-peer messages.
//
// Pre-2026-05-12 the protocol was "one conn.Write = one conn.Read":
// every message was sent as a raw byte slice and the receiver
// assumed each Read returned exactly one message. That held for
// dmsg (noise-framed up to 4 KB) but broke on the skynet route
// path — appnet.directConn wraps transport.VStream, which is a
// TCP-style stream that can split a single Write across multiple
// Reads at arbitrary boundaries depending on route MTU. A
// 600-byte chat message would arrive as two "messages" on the
// receiver and the second half would surface as a separate chat
// entry, looking to operators like the message was truncated.
//
// New protocol: length-prefixed frames. Each message is a 4-byte
// big-endian length followed by exactly that many bytes of
// payload. Old binaries can no longer talk to new ones — peers
// must update together. The pair-control envelope (JSON `{type:
// "pair-invite" | ...}`) keeps the same on-the-wire bytes; only
// the framing around it changed.
const skychatMaxFrameSize = 64 * 1024

// framedConn wraps an appnet conn with length-prefixed framing
// and a write mutex. The write mutex matters because two
// callers (the HTTP /message handler and the pair-control
// sender) can race to write to the same underlying conn — and
// with framing, interleaving the length prefix of one message
// with the payload of another would desync the receiver
// permanently. The read path has a single owner (handleConn)
// so no read mutex is needed.
type framedConn struct {
	net.Conn
	writeMu sync.Mutex
}

func newFramedConn(c net.Conn) *framedConn { return &framedConn{Conn: c} }

// WriteFrame writes a length-prefixed message. Returns an error
// if the payload is empty or exceeds skychatMaxFrameSize.
//
// Unbounded — if the underlying net.Conn's Write blocks (peer not
// draining, transport stuck), the call blocks indefinitely and the
// caller's request goroutine is wedged. messageHandler uses
// WriteFrameDeadline below instead so a slow peer can't pin the
// /message handler forever.
func (c *framedConn) WriteFrame(payload []byte) error {
	if len(payload) == 0 {
		return errors.New("skychat: empty payload")
	}
	if len(payload) > skychatMaxFrameSize {
		return fmt.Errorf("skychat: payload %d > max %d", len(payload), skychatMaxFrameSize)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := c.Conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Conn.Write(payload)
	return err
}

// messageWriteTimeout bounds a single WriteFrame call from the
// /message HTTP handler. Picked above the typical successful-send
// latency (low-ms) and below the operator-facing default ack budget
// (5–10 s), so a slow peer hits the deadline well before the caller
// gives up on the HTTP request entirely.
//
// Pre-fix the chat-app had no per-write deadline anywhere — a peer
// whose underlying transport stalled (e.g. dmsg session half-dead
// because the peer's visor was crashlooping) would pin the
// /message handler's goroutine indefinitely on the inner
// c.Conn.Write(). The hung handler held c.writeMu, so every
// subsequent send on the SAME conn queued behind it. Worse,
// observed cross-peer wedge: a single Beta-send hang at 20:20Z
// preceded every subsequent /message request — to Alpha, Beta,
// any peer — timing out at the HTTP context-deadline (~30 s) for
// hours afterward. Bounding the inner Write lets the handler
// surface a clean timeout, the writeMu releases, and the next
// caller's send is freed.
const messageWriteTimeout = 5 * time.Second

// WriteFrameDeadline is the bounded variant used from
// messageHandler. Sets a write deadline on the underlying net.Conn
// for the duration of this Write call, then resets it on the way
// out — so the deadline scope is exactly the framed write and
// future calls aren't accidentally pre-expired by a lingering
// deadline. A net.Conn-level timeout surfaces as an error from
// Write whose Timeout() method returns true, which the caller can
// distinguish from a connection-reset or peer-close.
//
// timeout must be > 0; pass 0 to fall back to plain WriteFrame
// (no deadline). The reset uses time.Time{} which net.Conn
// documents as "no deadline".
func (c *framedConn) WriteFrameDeadline(payload []byte, timeout time.Duration) error {
	if timeout <= 0 {
		return c.WriteFrame(payload)
	}
	// Acquire writeMu BEFORE setting the deadline so the deadline
	// scope tracks the actual on-wire write rather than including
	// time spent waiting behind a concurrent same-peer writer. If
	// the wait itself wedges, the caller's HTTP context-deadline
	// is the outer ceiling.
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.Conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		// SetWriteDeadline failure is rare on a healthy net.Conn;
		// fall through to a best-effort unbounded write rather
		// than blocking the message entirely on a metadata error.
		// Subsequent SetWriteDeadline(time.Time{}) reset on the
		// way out is also best-effort.
		_ = err //nolint:errcheck
	}
	defer func() {
		_ = c.Conn.SetWriteDeadline(time.Time{}) //nolint:errcheck
	}()

	if len(payload) == 0 {
		return errors.New("skychat: empty payload")
	}
	if len(payload) > skychatMaxFrameSize {
		return fmt.Errorf("skychat: payload %d > max %d", len(payload), skychatMaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := c.Conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Conn.Write(payload)
	return err
}

// dialAndCache dials the peer at addr (using the package retrier),
// wraps the raw conn in framing, registers it in the conns cache,
// and starts the read loop. Used both from the cache-miss path in
// messageHandler and from the redial-after-stale-write path. The
// receive loop's lifetime is the underlying peer connection, not
// the calling HTTP request — req.Context() cancel must NOT tear it
// down, so we deliberately pass a long-lived context (the handler's
// ctx, scoped to the whole app run).
func dialAndCache(ctx context.Context, pk cipher.PubKey, addr appnet.Addr) (*framedConn, error) {
	var raw net.Conn
	err := r.Do(ctx, func() error {
		c, dialErr := appCl.Dial(addr)
		if dialErr != nil {
			return dialErr
		}
		raw = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	conn := newFramedConn(raw)
	connsMu.Lock()
	conns[pk] = conn
	connsMu.Unlock()
	go handleConn(conn) //nolint:gosec // G118: long-lived conn, not request-scoped
	return conn, nil
}

// tryNetworkFallback is the last-resort dial used by messageHandler
// when the primary network's send (cached + redial+retry) failed.
// Dials the OTHER network (skynet ↔ dmsg) once, caches the conn on
// success, and returns it for the caller to write through.
//
// Returns (nil, _) when:
//   - the alternate network isn't enabled in this chat-app run (the
//     operator passed --skynet=false or --dmsg=false at launch)
//   - the alternate-network dial itself fails (peer doesn't listen
//     there, transport down, etc.)
//
// On success, also returns the appnet.Addr used so the caller can
// label the fallback in logs / counters and update its local
// netType state. The caller is responsible for the WriteFrame +
// per-leg accounting; this helper only opens the conn.
//
// Implementation deliberately does NOT consult the cache for the
// alternate network: by definition the primary network's cached
// conn just failed; the alternate's cache is independent and may be
// stale too. A fresh dial is the safest bet on a fallback path.
// (We do still register the new conn in the conns map by PK, which
// means the next /message to the same PK will find this conn —
// per-conn caching is keyed by PK only, not by netType, so the
// fallback "wins" until the operator's next chat-app restart or a
// stale-write evicts it.)
func tryNetworkFallback(ctx context.Context, pk cipher.PubKey, currentNet appnet.Type) (*framedConn, appnet.Addr) {
	var altNet appnet.Type
	switch currentNet {
	case appnet.TypeSkynet:
		altNet = appnet.TypeDmsg
		if !useDmsg {
			return nil, appnet.Addr{}
		}
	case appnet.TypeDmsg:
		altNet = appnet.TypeSkynet
		if !useSkynet {
			return nil, appnet.Addr{}
		}
	default:
		return nil, appnet.Addr{}
	}
	altAddr := appnet.Addr{Net: altNet, PubKey: pk, Port: 1}
	conn, err := dialAndCache(ctx, pk, altAddr)
	if err != nil {
		chatLog.Debugf("Network-fallback dial %s → %s failed: %v", currentNet, altNet, err)
		return nil, altAddr
	}
	return conn, altAddr
}

// ReadFrame reads exactly one length-prefixed message. Rejects
// frames over skychatMaxFrameSize so a malicious or out-of-sync
// peer can't allocate gigabytes of memory by claiming a giant
// length.
func (c *framedConn) ReadFrame() ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.Conn, hdr[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length == 0 {
		return nil, errors.New("skychat: zero-length frame")
	}
	if length > skychatMaxFrameSize {
		return nil, fmt.Errorf("skychat: frame %d > max %d (peer running old unframed protocol?)", length, skychatMaxFrameSize)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.Conn, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

var (
	addr   string
	appCl  *app.Client
	appLog func(format string, args ...interface{}) // App logger function
	// chatLog is the package-level logrus logger every code path can
	// reach without going through appCl. In visor-launched mode it
	// proxies through appCl.Log(); in --standalone mode appCl is nil
	// and chatLog holds the stderr logrus instance directly. HTTP
	// handlers + SSE pumps + accept loops use chatLog to avoid the
	// nil-deref crash class on standalone.
	chatLog   logrus.FieldLogger
	hub       *sseHub                       // SSE broadcast registry; see sse.go-like helpers below
	conns     map[cipher.PubKey]*framedConn // Chat connections
	connsMu   sync.Mutex
	appPort   uint16
	useSkynet bool
	useDmsg   bool

	// standalone mode: skip the visor app-launcher handshake
	// (PROC_CONFIG env, app.NewClient) entirely. Skynet + dmsg
	// transports become no-ops (they need the visor RPC channel);
	// TCP-direct + the HTTP control surface remain functional. Used
	// to run a long-lived chat-app process that survives visor
	// restarts — the reliability-floor recipe per Alpha's
	// 2026-05-18 design + #2707 noise-TCP listener.
	standalone bool

	// Optional HTTP password gate. When --password-file points at a
	// file containing a bcrypt hash, every HTTP endpoint requires
	// matching basic auth (or the hypervisor's internal-proxy
	// bypass token, see auth.go). Empty file or missing flag →
	// no auth, current behavior.
	passwordFile  string
	internalToken string

	// Persistence (Phase 1) — all off by default.
	persistEnabled       bool
	persistDBPath        string
	persistMaxMsgSize    int
	persistPerPeerRate   int
	persistPerPeerCap    int
	persistTotalCapMB    int
	persistTTLDays       int
	persistWhitelistFile string
	persistSeedCount     int

	// historyStore is nil when persistence is disabled.
	historyStore history.Store

	// runtime counters surfaced via /status — single mutex covers
	// all of them since they're updated in lockstep with sse hub
	// activity (well-bounded contention). counterMu is held only
	// during the assignment, never spanning I/O.
	counterMu        sync.Mutex
	startedAt        = time.Now()
	lastRxAt         time.Time // most recent inbound chat message (incl. self-loop)
	lastSendAt       time.Time // most recent successful outgoing send
	inboundMsgCount  uint64
	outboundMsgCount uint64
	inboundDropCount uint64 // ReadFrame errors / unparseable frames on the inbound path

	// outboundFailCount is the # of /message requests where the
	// write to the peer conn could not be delivered — counts the
	// failure that finally surfaces to the HTTP caller, after the
	// in-handler redial+retry path has already given up.
	//
	// outboundRetryCount is how many /message requests took the
	// redial-after-stale-conn path. A healthy steady state has this
	// ~= 0; a non-zero rate means peers' transports are flapping
	// (or visor restarts are tearing them down) and the in-handler
	// retry is masking the symptom. Operators with retry > 0 and
	// fail == 0 means "we papered over the flap"; retry > 0 and
	// fail > 0 means "the retry isn't winning".
	outboundFailCount  uint64
	outboundRetryCount uint64

	// sseDropCount counts messages the SSE hub dropped because a
	// subscriber's per-client buffer was full at broadcast time.
	// Each drop is a message that one listener never saw — climbs
	// when a CLI listener stalls (terminal scrollback paused) or
	// a browser tab is backgrounded long enough to drift behind
	// sseSubscriberBufSize. Surfaced in /status so operators can
	// tell "my listener missed N msgs" without log-scraping.
	sseDropCount uint64

	// outboundFallbackCount is how many /message requests succeeded
	// only after falling over from the primary network (skynet by
	// default) to the alternate (dmsg, and vice versa).
	//
	// Skychat listens on BOTH networks by default, so a peer who
	// receives on one can usually be reached on the other. When the
	// chosen network's WriteFrame fails after the redial+retry path
	// exhausts itself, the handler dials the alternate network once
	// and writes again before surfacing the failure. Successful
	// rescues increment this counter; full failures (both networks
	// dead) still increment outboundFailCount as usual.
	//
	// Operators with outbound_fallback_count > 0 are seeing the
	// chosen-network path break often enough to need the safety
	// net. Persistent non-zero rate is a signal to investigate the
	// primary network (route flap, dmsg server churn) — the
	// fallback is rescuing user-visible delivery but at the cost of
	// an extra dial per affected message.
	outboundFallbackCount uint64
)

// frameProtoVersion is the on-the-wire protocol version this chat
// app speaks. Bumped on any frame-layout change (envelope shape,
// new mandatory fields). Surfaced via /status so operators rolling
// staggered deploys can spot version skew before it manifests as
// confusing wire failures.
//
// version 1 — initial length-prefixed framed wire (post-#2504).
//
//	chat-msg envelope (pre-2026-05-12 plain bytes) and
//	pair-control JSON envelope co-exist as before; this
//	version is just for diagnostic visibility.
const frameProtoVersion = 1

// schemaVersion is the listen-output JSON schema version emitted on
// banner events. Distinct from frameProtoVersion (which is the
// on-the-wire chat-frame format) — the schema covers listener-side
// event shape. Bump on any breaking change to msg/banner/reconnect/
// error event field semantics (renaming, removing, type change).
// Additive field changes are NOT a bump.
const schemaVersion = "v1"

// newEventID returns a hex-encoded 64-bit random id, used to tag
// each SSE-broadcast msg event. Stable identifier consumers can use
// for log correlation, dedup, and (post-#65) ack correlation.
//
// 8 bytes of entropy gives ~2^32 collision avoidance under birthday
// paradox — overkill for what's effectively a per-process correlation
// id with a lifetime of seconds.
func newEventID() string {
	var buf [8]byte
	_, _ = cryptoRand.Read(buf[:]) //nolint:errcheck
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf[:]))
}

// the go embed static points to skywire/cmd/apps/skychat/static

//go:embed static
var embededFiles embed.FS

// sseSubscriberBufSize is the per-client outbound message buffer
// depth. A slow SSE client (or a stalled browser tab) drops messages
// once its buffer is full rather than blocking the producer; missed
// messages are recoverable from the replay buffer on reconnect.
const sseSubscriberBufSize = 64

// sseReplayBufSize is the depth of the ring buffer of recent
// broadcasts kept for replay to listeners that connect after the
// messages were broadcast. Sized to cover a few minutes of typical
// chat traffic so a CLI listener that disconnected briefly (visor
// cycle, network blip) picks back up where it left off rather than
// silently losing the window's messages.
const sseReplayBufSize = 256

// sseHub fans messages out to every connected SSE client. The
// previous implementation used a single unbuffered channel, which
// meant exactly ONE consumer received each message — when more than
// one tab was open, or a stale handler was leaked, every other tab
// silently lost messages. The hub registers a per-client channel on
// connect and broadcasts to all of them on each message.
//
// To make the "listener reconnected after disconnect" case lossless,
// the hub also keeps a ring buffer of the last sseReplayBufSize
// messages. New subscribers receive that history before live
// broadcasts begin.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}

	// Ring buffer for replay. `head` is the next write slot; `count`
	// is how many slots are populated (saturates at len(replay)).
	replay     []string
	replayHead int
	replayLen  int
}

func newSSEHub() *sseHub {
	return &sseHub{
		clients: make(map[chan string]struct{}),
		replay:  make([]string, sseReplayBufSize),
	}
}

// subscribe registers a fresh client channel and returns it plus an
// unsubscribe func the caller MUST invoke on shutdown. The channel
// is buffered so a producer never blocks on a slow consumer.
//
// Before live broadcasts start flowing, the new subscriber's buffer
// is pre-filled with whatever messages remain in the hub's replay
// ring — so a reconnecting CLI listener picks up the recent history
// it missed during disconnect.
func (h *sseHub) subscribe() (<-chan string, func()) {
	ch := make(chan string, sseSubscriberBufSize)
	h.mu.Lock()
	// Drain replay buffer into the new client's channel. Order is
	// oldest → newest. We bound by the channel's buffer size so we
	// don't block; any overflow is silently truncated (rare, since
	// sseReplayBufSize > sseSubscriberBufSize means only the most
	// recent sseSubscriberBufSize messages survive).
	if h.replayLen > 0 {
		start := (h.replayHead - h.replayLen + len(h.replay)) % len(h.replay)
	replayLoop:
		for i := 0; i < h.replayLen; i++ {
			idx := (start + i) % len(h.replay)
			select {
			case ch <- h.replay[idx]:
			default:
				break replayLoop
			}
		}
	}
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// clientCount returns how many SSE subscribers are currently
// connected. Used by /status for operator health probes.
func (h *sseHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// broadcast sends msg to every connected client. Drops to clients
// whose buffer is full — bounded fan-out keeps a single stalled
// client from holding back the whole stream. The msg is also
// appended to the replay ring buffer so a future subscriber can
// pick up history they missed during disconnect.
//
// When NO subscribers are connected at broadcast time, the message
// still lands in the replay buffer (so a reconnecting listener
// recovers it) but ALSO ticks sseDropCount by 1 to surface the
// "listener was offline" condition in /status. Pre-fix the empty-
// subscribers case was an invisible silent drop — operators saw
// inbound_msg_count rise without sse_drop_count moving and assumed
// reliable delivery; that was wrong.
func (h *sseHub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Always record in the replay ring, regardless of live-subscriber
	// state. The ring overwrites oldest-first when full.
	h.replay[h.replayHead] = msg
	h.replayHead = (h.replayHead + 1) % len(h.replay)
	if h.replayLen < len(h.replay) {
		h.replayLen++
	}

	var drops uint64
	if len(h.clients) == 0 {
		// No live readers right now. Replay buffer will catch any
		// listener that reconnects within the next sseReplayBufSize
		// messages; surface the "no subscribers" event in /status
		// so the operator knows live-stream had a gap.
		drops = 1
	} else {
		for ch := range h.clients {
			select {
			case ch <- msg:
			default:
				drops++
			}
		}
	}
	if drops > 0 {
		counterMu.Lock()
		sseDropCount += drops
		counterMu.Unlock()
	}
}

func init() {
	launcher.RegisterApp("skychat", RunSkychat)
	RootCmd.Flags().StringVar(&addr, "addr", ":8001", "address to bind (default: localhost-only); use \"*:PORT\" to bind on all interfaces")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().BoolVar(&useSkynet, "skynet", true, "listen on skynet network")
	RootCmd.Flags().BoolVar(&useDmsg, "dmsg", true, "listen on dmsg network")
	RootCmd.Flags().StringVar(&passwordFile, "password-file", "", "path to a file containing a bcrypt hash; when set, gates HTTP endpoints with basic auth")
	RootCmd.Flags().StringVar(&internalToken, "internal-token", "", "shared secret used by the hypervisor's reverse proxy to bypass the password gate; managed automatically by the visor")

	// Persistence flags (Phase 1). All default off; when --persist is set,
	// the others fall back to conservative defaults.
	RootCmd.Flags().BoolVar(&persistEnabled, "persist", false, "persist chat history to a local BoltDB (off by default)")
	RootCmd.Flags().StringVar(&persistDBPath, "persist-db", "", "path to the BoltDB file (default: <work-dir>/skychat-history.db)")
	RootCmd.Flags().IntVar(&persistMaxMsgSize, "persist-max-size", 4096, "maximum persisted message size in bytes")
	RootCmd.Flags().IntVar(&persistPerPeerRate, "persist-per-peer-rate", 20, "persisted messages per minute per peer (rate limit)")
	RootCmd.Flags().IntVar(&persistPerPeerCap, "persist-per-peer-cap", 500, "maximum persisted messages per peer (FIFO eviction)")
	RootCmd.Flags().IntVar(&persistTotalCapMB, "persist-total-cap", 10, "total persisted storage cap in MB")
	RootCmd.Flags().IntVar(&persistTTLDays, "persist-ttl", 30, "days to keep persisted messages before sweep (0 disables)")
	RootCmd.Flags().StringVar(&persistWhitelistFile, "persist-whitelist", "", "path to file with one peer PK per line; if set, only these peers are persisted")
	RootCmd.Flags().IntVar(&persistSeedCount, "persist-seed", 50, "number of recent messages to seed new SSE clients with (0 disables)")

	// Pairing flags. Off by default so the legacy plain-text DM path
	// (used by the e2e CI tests) is unaffected. When --pair-enable is
	// on, skychat dials the local visor's RPC and exposes the
	// chat-pair feed manager over HTTP + the structured pair-invite
	// / pair-ack protocol over the legacy direct path.
	RootCmd.Flags().BoolVar(&pairEnable, "pair-enable", false, "enable per-partner CXO pair feeds (HTTP /pair endpoints + handshake)")
	RootCmd.Flags().StringVar(&pairRPCAddr, "pair-rpc", "localhost:3435", "visor RPC address used by the pair manager")
	RootCmd.Flags().DurationVar(&pairPollInterval, "pair-poll-interval", time.Second, "how often skychat drains the visor's pair-message inbox onto the SSE stream")

	// TCP-direct entry points — see tcp_direct.go. Defaults disabled.
	RootCmd.Flags().StringVar(&tcpListen, "tcp-listen", "", "accept noise-XK on TCP (e.g. ':8800'); needs an identity (--sk/-c/env). --tcp-whitelist optional (empty = open to any authenticated key). Bidirectional once established.")
	RootCmd.Flags().StringSliceVar(&tcpPeers, "tcp-peer", nil, "persistent outbound TCP-direct peer: tcp://<pk>@host:port (repeat for many). For NAT-side hosts that dial out to public-IP peers.")
	RootCmd.Flags().StringVar(&tcpWhitelist, "tcp-whitelist", "", "comma-separated peer PKs allowed to connect via --tcp-listen (empty = open to any authenticated key, matching skynet/CXO convention)")
	RootCmd.Flags().StringVar(&tcpSKFlag, "sk", "", "identity SK for TCP-direct (hex). Overrides env + config.")
	RootCmd.Flags().StringVarP(&tcpConfigPath, "config", "c", "", "path to skywire.json — only the sk field is read, for TCP-direct / CXO identity")
	RootCmd.Flags().BoolVar(&cxoEnable, "cxo", false, "enable CXO-backed messaging over native TCP (no dmsg): publish outbound to your CXO feed, subscribe to --cxo-peer feeds. Works in --standalone.")
	RootCmd.Flags().StringVar(&cxoListen, "cxo-listen", ":8802", "CXO-TCP listen address for your feed (peers dial this)")
	RootCmd.Flags().StringSliceVar(&cxoPeers, "cxo-peer", nil, "subscribe to a peer's CXO feed: tcp://<feedpk>@host:port (repeat for many)")
	RootCmd.Flags().StringVar(&cxoGroup, "cxo-group", "", "enable federated CXO GROUP chat with this group id (over native TCP, roster/signing/gossip); members from --cxo-peer, owner from --cxo-group-owner")
	RootCmd.Flags().StringVar(&cxoGroupOwner, "cxo-group-owner", "", "group owner PK (your role is owner if it equals your identity, else member)")
	RootCmd.Flags().BoolVar(&standalone, "standalone", false, "run without a parent visor: skip PROC_CONFIG handshake, disable skynet/dmsg listenLoops, keep --tcp-listen/--tcp-peer + the HTTP control surface. Pair-RPC endpoints become 503 (no visor pair-rpc to relay through). Use this to run a long-lived chat-app that survives visor restarts — reachable via TCP-direct only.")
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use:                   "skychat",
	Short:                 "skywire chat application",
	Long:                  calvin.AsciiFont("skychat"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()

		if err := RunSkychat(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// RunSkychat runs the skychat app logic. This can be called from the visor or from the CLI.
func RunSkychat(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		// Create independent FlagSet for parsing without initialization cycle
		fs := pflag.NewFlagSet("skychat", pflag.ContinueOnError)
		fs.StringVar(&addr, "addr", ":8001", "address to bind")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		fs.BoolVar(&useSkynet, "skynet", true, "listen on skynet")
		fs.BoolVar(&useDmsg, "dmsg", true, "listen on dmsg")
		fs.StringVar(&passwordFile, "password-file", "", "path to bcrypt hash for HTTP basic auth")
		fs.StringVar(&internalToken, "internal-token", "", "hypervisor proxy bypass token")
		fs.BoolVar(&persistEnabled, "persist", false, "persist chat history to BoltDB")
		fs.StringVar(&persistDBPath, "persist-db", "", "path to BoltDB file")
		fs.IntVar(&persistMaxMsgSize, "persist-max-size", 4096, "max message size bytes")
		fs.IntVar(&persistPerPeerRate, "persist-per-peer-rate", 20, "per-peer rate limit / min")
		fs.IntVar(&persistPerPeerCap, "persist-per-peer-cap", 500, "per-peer message cap")
		fs.IntVar(&persistTotalCapMB, "persist-total-cap", 10, "total storage cap in MB")
		fs.IntVar(&persistTTLDays, "persist-ttl", 30, "days to keep persisted messages")
		fs.StringVar(&persistWhitelistFile, "persist-whitelist", "", "whitelist file path")
		fs.IntVar(&persistSeedCount, "persist-seed", 50, "messages to seed SSE clients with")
		fs.BoolVar(&pairEnable, "pair-enable", false, "enable per-partner CXO pair feeds")
		fs.StringVar(&pairRPCAddr, "pair-rpc", "localhost:3435", "visor RPC address for pair manager")
		fs.DurationVar(&pairPollInterval, "pair-poll-interval", time.Second, "pair inbox poll interval")
		// TCP-direct flags must be parseable in the visor-launched
		// path too — visor passes args verbatim from skywire.json.
		fs.StringVar(&tcpListen, "tcp-listen", "", "TCP-direct listen addr")
		fs.StringSliceVar(&tcpPeers, "tcp-peer", nil, "tcp://<pk>@host:port (repeatable)")
		fs.StringVar(&tcpWhitelist, "tcp-whitelist", "", "comma-separated allowed peer PKs")
		fs.StringVar(&tcpSKFlag, "sk", "", "TCP-direct identity SK (hex)")
		fs.StringVarP(&tcpConfigPath, "config", "c", "", "skywire.json path for SK")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	// Wrap context with cancel to allow graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if standalone {
		// Standalone: don't talk to the visor app-launcher at all.
		// PROC_CONFIG isn't present, the visor's appserver isn't
		// reachable, and the only valid transports are TCP-direct
		// (--tcp-listen / --tcp-peer). Force skynet/dmsg off so the
		// listenLoop attempts don't try to acquire a nil rpcC.
		useSkynet = false
		useDmsg = false
		standaloneLog := logrus.New()
		standaloneLog.SetOutput(os.Stderr)
		standaloneLog.SetFormatter(&logging.TextFormatter{
			FullTimestamp:      true,
			AlwaysQuoteStrings: true,
			QuoteEmptyFields:   true,
			ForceFormatting:    true,
			DisableColors:      false,
			ForceColors:        true,
			TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
		})
		chatLog = standaloneLog.WithField("_module", "skychat-standalone")
		appLog = func(format string, args ...interface{}) {
			chatLog.Infof(format, args...)
		}
	} else {
		appCl = app.NewClient(nil)
		defer appCl.Close()
		chatLog = appCl.Log()
		appLog = func(format string, args ...interface{}) {
			appCl.Log().Infof(format, args...)
		}
	}

	appLog("Build info: %s", buildinfo.Version())
	if standalone {
		appLog("Successfully started skychat in --standalone mode (no visor handshake; TCP-direct + HTTP control surface only).")
	} else {
		appLog("Successfully started skychat.")
	}

	if persistEnabled {
		if err := openHistoryStore(); err != nil {
			appLog("Failed to open history store: %v — continuing in ephemeral mode", err)
		} else {
			defer func() {
				if historyStore != nil {
					if err := historyStore.Close(); err != nil {
						appLog("history store close: %v", err)
					}
				}
			}()
		}
	}

	hub = newSSEHub()

	// In standalone mode there is no visor-assigned routing port and
	// no AppCl to register a port with. Set a placeholder so any code
	// that reads `port` for log lines stays well-defined; nothing in
	// the standalone code path actually dials via this number.
	var port routing.Port
	if appCl != nil {
		port = appCl.Config().RoutingPort
		if appPort != 0 {
			port = routing.Port(appPort)
			appCl.SetAppPortOrLog(port)
		}
	} else if appPort != 0 {
		port = routing.Port(appPort)
	}

	conns = make(map[cipher.PubKey]*framedConn)

	if useSkynet {
		go listenLoop(appnet.TypeSkynet, port)
	}
	if useDmsg {
		go listenLoop(appnet.TypeDmsg, port)
	}
	// TCP-direct entry point — independent of useSkynet/useDmsg.
	// Operator opts in via --tcp-listen / --tcp-peer; nil-effect
	// when both are empty so existing visor-managed setups are
	// unaffected. See tcp_direct.go for the accept/dial loops.
	if err := startTCPDirect(ctx); err != nil {
		appLog("skychat: tcp-direct startup failed: %v — continuing with dmsg/skynet only", err)
	}
	// CXO-backed messaging entry point (native TCP, no dmsg). Opt-in via
	// --cxo; nil-effect when unset. See cxo_tcp.go.
	if err := startCXOTCP(ctx); err != nil {
		appLog("skychat: cxo startup failed: %v — continuing without CXO mode", err)
	}
	if !useSkynet && !useDmsg && tcpListen == "" && len(tcpPeers) == 0 && !cxoEnable && cxoGroup == "" {
		appLog("Warning: no network types enabled, skychat will not accept connections")
	}

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(skyenv.SkychatName, nil)
		if err != nil {
			appLog("Error creating ipc server for skychat client: %v", err)
			appCl.SetErrorOrLog(err)
			return err
		}
		go handleIPCSignal(ipcClient)
	}

	connectPairRPC()
	startPairRPCWatchdog(ctx)
	defer stopPairRPCWatchdog()
	startPairPoller(ctx)
	defer stopPairPoller()

	// Wire optional password protection. If passwordFile is empty or
	// the file is missing, requireAuth* are no-ops.
	if err := loadSkychatPassword(passwordFile); err != nil {
		appLog("password file load: %v — continuing without auth", err)
	}
	setSkychatInternalToken(internalToken)

	// Use a fresh local ServeMux so RunSkychat is safe to call more
	// than once in the same process (in-process launcher re-launch on
	// app restart, or any caller invoking it twice). The previous
	// http.DefaultServeMux registrations panicked on duplicate "/"
	// the second time around — same file, same line, same pattern —
	// taking the entire chat-app down whenever the launcher tried to
	// recover from a transient failure or any other re-launch path.
	mux := http.NewServeMux()
	mux.Handle("/", requireAuth(http.FileServer(getFileSystem())))
	mux.HandleFunc("/message", requireAuthFunc(messageHandler(ctx)))
	mux.HandleFunc("/sse", requireAuthFunc(sseHandler))
	mux.HandleFunc("/history", requireAuthFunc(historyHandler))
	mux.HandleFunc("/history/peers", requireAuthFunc(historyPeersHandler))
	mux.HandleFunc("/status", requireAuthFunc(statusHandler))
	registerPairHTTPHandlers(ctx, mux)

	url := ""
	address := addr
	if len(address) >= 2 && address[:2] == "*:" {
		url = "0.0.0.0" + address[1:]
	} else if len(address) >= 1 && address[:1] == ":" {
		url = "127.0.0.1" + address
	} else if host, port, err := net.SplitHostPort(address); err == nil && host != "" && port != "" {
		url = address
	} else {
		url = "127.0.0.1:8001"
	}

	appLog("Serving HTTP on %s", url)

	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			select {
			case <-termCh:
				appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
				cancel()
			case <-ctx.Done():
				appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
				return
			}
		}()
	}

	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
	srv := &http.Server{
		Addr:         url,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			appLog("HTTP server error: %v", err)
			appCl.SetErrorOrLog(err)
			return err
		}
	case <-ctx.Done():
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		if err := srv.Shutdown(context.Background()); err != nil {
			return err
		}
	}

	return nil
}

func listenLoop(netType appnet.Type, appPort routing.Port) {
	l, err := appCl.Listen(netType, appPort)
	if err != nil {
		appLog("Error listening network %v on port %d: %v", netType, appPort, err)
		appCl.SetErrorOrLog(err)
		return
	}

	appLog("Listening on %s network, port %d", netType, appPort)

	for {
		appCl.Log().Debugf("Accepting skychat conn on %s...", netType)
		conn, err := l.Accept()
		if err != nil {
			appLog("Failed to accept conn on %s: %v", netType, err)
			return
		}
		appCl.Log().Debugf("Accepted skychat conn on %s", netType)

		raddr := conn.RemoteAddr().(appnet.Addr)
		fc := newFramedConn(conn)
		connsMu.Lock()
		conns[raddr.PubKey] = fc
		connsMu.Unlock()
		appLog("Accepted skychat conn on %s from %s via %s", conn.LocalAddr(), raddr.PubKey, netType)

		go handleConn(fc)
	}
}

func handleConn(conn *framedConn) {
	raddr := conn.RemoteAddr().(appnet.Addr)
	for {
		payload, err := conn.ReadFrame()
		if err != nil {
			appLog("Failed to read packet: %v", err)
			counterMu.Lock()
			inboundDropCount++
			counterMu.Unlock()
			connsMu.Lock()
			delete(conns, raddr.PubKey)
			connsMu.Unlock()
			return
		}

		// First, try the pair-control envelope. Recognized types
		// (pair-invite / pair-ack) are consumed here and not surfaced
		// as chat messages; everything else falls through to the
		// legacy plain-text path so the existing CI tests are
		// unaffected.
		if handlePairControlFrame(context.Background(), raddr.PubKey, payload) {
			continue
		}

		peerPK := raddr.PubKey.Hex()

		// Then try the chat-msg / chat-ack envelope. A chat-msg
		// envelope (type=chat-msg, ack=true) → unwrap body, send
		// chat-ack back, surface body as a normal chat message.
		// A chat-ack envelope (type=chat-ack) → consumed internally
		// to satisfy a waiting /message --wait request, NOT surfaced
		// to the SSE stream. Plain-text bodies fall through to the
		// legacy path unchanged.
		envHandled, envBody, _ := tryHandleChatEnvelope(payload, peerPK, func(id string) {
			ackEnv := chatEnvelope{Type: chatTypeAck, ID: id}
			ackBytes, mErr := json.Marshal(ackEnv)
			if mErr != nil {
				appLog("chat-ack marshal failed: %v", mErr)
				return
			}
			if wErr := conn.WriteFrame(ackBytes); wErr != nil {
				appLog("chat-ack write to %s failed: %v", peerPK, wErr)
			}
		})
		var text string
		if envHandled {
			if envBody == "" {
				// chat-ack consumed — nothing to surface.
				continue
			}
			text = envBody
		} else {
			text = string(payload)
		}

		counterMu.Lock()
		inboundMsgCount++
		lastRxAt = time.Now().UTC()
		counterMu.Unlock()

		// Persist (best-effort, never blocks ephemeral path).
		persistMessage(history.Message{
			Peer:      peerPK,
			From:      peerPK,
			Outgoing:  false,
			Text:      text,
			Timestamp: time.Now().UTC(),
		})

		// Include the network field so listen --net can filter
		// inbound just as it can filter outbound. Without this, SSE
		// events for incoming messages carry no transport tag and any
		// consumer-side --net filter drops them all.
		//
		// "dir" is a string ("in"|"out"|... future "relay"|"group-in"|
		// "group-out") rather than an "outgoing" bool so the schema
		// stays extensible as group / relay flows land without a
		// breaking wire-version bump.
		//
		// "id" is a stable per-event identifier. Pre-#65-ack: no one
		// consumes it; post-#65-ack: send --wait references it to
		// correlate inbound chat-ack envelopes back to the originating
		// send without a separate schema bump. Today it's just a UUID
		// so consumers can already use it for log correlation / dedup.
		// "len" is the body byte length, surfaced for size-debug
		// without forcing consumers to count after a json round-trip.
		clientMsg, err := json.Marshal(map[string]interface{}{
			"sender":  peerPK,
			"message": text,
			"network": string(raddr.Net),
			"dir":     "in",
			"id":      newEventID(),
			"len":     len(text),
		})
		if err != nil {
			appLog("Failed to marshal json: %v", err)
		}
		hub.broadcast(string(clientMsg))
	}
}

func messageHandler(ctx context.Context) func(w http.ResponseWriter, rreq *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {

		var data struct {
			Recipient string `json:"recipient"`
			Message   string `json:"message"`
			Network   string `json:"network,omitempty"`
			// WaitMS, if positive, requests peer-receipt acknowledgment:
			// the message is wrapped in a chat-msg envelope with a
			// unique id, written to the peer, and this handler blocks
			// up to WaitMS for the peer's chat-ack envelope to come
			// back over the same conn. On ack: returns 200 + JSON
			// {acked:true, ms:<elapsed>}. On timeout: 504 + JSON
			// {acked:false, reason:"timeout"}. Clamped to
			// [chatAckTimeoutFloor, chatAckTimeoutCeiling] server-side.
			WaitMS int `json:"wait_ms,omitempty"`
		}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(data.Recipient)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Determine network type - default to skynet, allow dmsg
		netType := appnet.TypeSkynet
		if data.Network != "" {
			switch data.Network {
			case "dmsg":
				netType = appnet.TypeDmsg
			case "skynet":
				netType = appnet.TypeSkynet
			default:
				http.Error(w, "invalid network type: use 'skynet' or 'dmsg'", http.StatusBadRequest)
				return
			}
		}

		addr := appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   1,
		}

		// CXO-backed mode: publish outbound to our own CXO feed; every
		// peer subscribed to our feed receives it. No per-message ack
		// (CXO is eventual) — success means the leaf was published. This
		// short-circuits the tcp-direct/skynet/dmsg send path below.
		if cxoEnable || cxoGroup != "" {
			path, perr := publishCXO(data.Message)
			if perr != nil {
				http.Error(w, fmt.Sprintf("cxo publish: %v", perr), http.StatusServiceUnavailable)
				return
			}
			cxoMu.Lock()
			myPK := cxoMyPK
			cxoMu.Unlock()
			outMsg, _ := json.Marshal(map[string]interface{}{ //nolint:errcheck
				"sender":    myPK.Hex(),
				"recipient": data.Recipient,
				"message":   data.Message,
				"network":   "cxo",
				"dir":       "out",
				"id":        newEventID(),
				"len":       len(data.Message),
				"path":      path,
			})
			hub.broadcast(string(outMsg))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true,"network":"cxo"}`))
			return
		}

		// Self-send detection — log it clearly so the user can tell self
		// traffic apart from peer traffic in the visor log. A dmsg self-
		// dial loops back through the visor's own delegated dmsg server:
		// the server bridges a new outbound yamux stream back to the same
		// client, and the local listener accepts it. The dial here is
		// the real network path — no local short-circuit. Same for
		// skynet (router builds a 2-hop self-ping loopback).
		// Self-detection requires the visor-supplied VisorPK; in
		// --standalone mode appCl is nil and there's no self-loop
		// path to special-case (TCP-direct handles loopback by host:port).
		isSelf := false
		if appCl != nil {
			isSelf = pk == appCl.Config().VisorPK
		}
		if isSelf {
			chatLog.Infof("Self-send via %s on port %d", netType, addr.Port)
		}

		// cached tells the write path whether the conn came from
		// the cache (worth a redial+retry on stale-conn write
		// errors) or from a fresh dial below (write failure on a
		// fresh dial means the path is really broken — retrying is
		// unlikely to help and just doubles the surfaced latency).
		connsMu.Lock()
		conn, cached := conns[pk]
		connsMu.Unlock()

		if !cached {
			// In --standalone mode appCl is nil — dialAndCache would
			// panic on appCl.Dial(). Skynet/dmsg listenLoops are off
			// in standalone (see RunSkychat above), so the only way
			// /message can reach a peer is through an already-cached
			// TCP-direct conn (populated by --tcp-peer outbound or
			// --tcp-listen accept). Surface a clean 503 with the
			// fix-it-yourself hint instead of a panic.
			if appCl == nil {
				http.Error(w,
					fmt.Sprintf("standalone mode: no cached tcp-direct conn to %s "+
						"(use --tcp-peer to establish a persistent outbound, "+
						"or 'skywire cli skychat send --via tcp://<pk>@host:port' to dial directly)", pk),
					http.StatusServiceUnavailable)
				return
			}
			var err error
			conn, err = dialAndCache(ctx, pk, addr)
			if err != nil {
				if isSelf {
					chatLog.WithError(err).Errorf("Self-dial via %s failed", netType)
				} else {
					chatLog.WithError(err).Warnf("Dial to %s via %s failed", pk, netType)
				}
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Build the on-the-wire payload. Default is plain text (back-
		// compat with every binary that has shipped to date). When
		// wait_ms is set, we wrap in a chat-msg envelope with a fresh
		// id, register an ack-waiter, and after the write blocks the
		// HTTP request on the ack channel up to wait_ms.
		wirePayload := []byte(data.Message)
		var ackID string
		var ackCh <-chan struct{}
		var unregisterAck func()
		var ackWait time.Duration
		if data.WaitMS > 0 {
			ackID = newEventID()
			env := chatEnvelope{Type: chatTypeMsg, ID: ackID, Body: data.Message, Ack: true}
			eb, mErr := json.Marshal(env)
			if mErr != nil {
				http.Error(w, mErr.Error(), http.StatusInternalServerError)
				return
			}
			wirePayload = eb
			ackWait = clampAckWait(time.Duration(data.WaitMS) * time.Millisecond)
			ackCh, unregisterAck = registerAckWaiter(ackID)
			defer unregisterAck()
		}

		// Write the framed payload. On a *cached* conn, a write
		// failure usually means the underlying transport went stale
		// between sends (peer visor restart, route churn) — the
		// frame is lost AND every subsequent send would also fail
		// until something kicks the cache. To get the operator out
		// of that hole inside the same request, we redial + retry
		// exactly once when the first attempt was on a cached conn.
		// A fresh-dial write failure isn't retried (a second dial
		// of the same network in the same handler tick won't help).
		writeStart := time.Now()
		err := conn.WriteFrameDeadline(wirePayload, messageWriteTimeout)
		if err != nil && cached {
			// Stale-conn auto-retry: a cached conn whose underlying
			// transport has gone stale (peer chat-app restart,
			// transient transport fault) used to lose the caller's
			// CURRENT message — handler returned 400, operator had
			// to retry from CLI/UI. Now we delete the stale entry
			// (pointer-eq guarded so we don't clobber a fresh conn
			// installed by a concurrent handler), dial fresh via
			// dialAndCache, and retry the WriteFrame ONCE. Only on a
			// second failure do we surface the error to the caller.
			// Fresh-dial write failures (cached==false branch below)
			// are NOT retried — a same-network second dial in the
			// same handler tick won't help and just doubles latency.
			connsMu.Lock()
			if conns[pk] == conn {
				delete(conns, pk)
			}
			connsMu.Unlock()

			chatLog.Debugf("Stale-conn write to %s via %s: %v — redialing", pk, netType, err)
			newConn, derr := dialAndCache(ctx, pk, addr)
			if derr != nil {
				counterMu.Lock()
				outboundFailCount++
				counterMu.Unlock()
				http.Error(w, fmt.Sprintf("redial after write %v: %v", err, derr), http.StatusBadRequest)
				return
			}
			if werr := newConn.WriteFrameDeadline(wirePayload, messageWriteTimeout); werr != nil {
				connsMu.Lock()
				if conns[pk] == newConn {
					delete(conns, pk)
				}
				connsMu.Unlock()
				counterMu.Lock()
				outboundFailCount++
				counterMu.Unlock()
				http.Error(w, fmt.Sprintf("retry after %v: %v", err, werr), http.StatusBadRequest)
				return
			}
			counterMu.Lock()
			outboundRetryCount++
			counterMu.Unlock()
			conn = newConn
			// Clear err so the post-retry block below doesn't double-
			// report a failure on a now-successful retry.
			err = nil
		}
		if err != nil {
			connsMu.Lock()
			if conns[pk] == conn {
				delete(conns, pk)
			}
			connsMu.Unlock()
			// Network fallback before surfacing the error to the
			// caller. Skychat listens on BOTH skynet and dmsg by
			// default (--skynet=true --dmsg=true), so a peer who
			// receives on one will usually receive on the other.
			// If the chosen network can't deliver, try the alternate
			// in one last attempt: dial fresh, write once, on
			// success continue to the normal mirror/ack/persist
			// path. On second failure, surface the original error
			// to the caller (the alternate's error is logged at
			// debug but not exposed, to keep the public error
			// stable across network-fallback fires/misses).
			//
			// Increment outboundFallbackCount on success so
			// operators can see how often the fallback path is
			// rescuing sends — a non-zero rate means the chosen
			// network is unreliable enough that callers should
			// reconsider the default, or the operator should fix
			// the transport / route to the peer on that network.
			fallbackOK := false
			if fbConn, fbAddr := tryNetworkFallback(ctx, pk, netType); fbConn != nil {
				if werr := fbConn.WriteFrameDeadline(wirePayload, messageWriteTimeout); werr == nil {
					counterMu.Lock()
					outboundFallbackCount++
					counterMu.Unlock()
					// Successful fallback write — conn / netType
					// would-be-reassignments dropped because the
					// caller doesn't read them after this point
					// (the response is rendered from ackCh state,
					// not the conn directly). Keeping fbAddr
					// referenced under _ documents the intent.
					_ = fbAddr
					fallbackOK = true
				} else {
					connsMu.Lock()
					if conns[pk] == fbConn {
						delete(conns, pk)
					}
					connsMu.Unlock()
					chatLog.Debugf("Network-fallback write to %s via %s also failed: %v",
						pk, fbAddr.Net, werr)
				}
			}
			if !fallbackOK {
				counterMu.Lock()
				outboundFailCount++
				counterMu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Persist outgoing message (best-effort).
		persistMessage(history.Message{
			Peer:      pk.Hex(),
			Outgoing:  true,
			Text:      data.Message,
			Timestamp: time.Now().UTC(),
		})

		// If --wait was requested, block on the ack channel. The
		// envelope was written above; the peer's chat-app, if it
		// speaks the chat-msg envelope (i.e. is post-2026-05-12),
		// recognizes it, persists the body, sends chat-ack back over
		// the same conn, which our handleConn routes to deliverAck →
		// our ackCh. Old peers see the JSON-encoded envelope as plain
		// text and never ack; we time out cleanly.
		if ackCh != nil {
			timer := time.NewTimer(ackWait)
			defer timer.Stop()
			select {
			case <-ackCh:
				elapsed := time.Since(writeStart)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
					"acked": true,
					"id":    ackID,
					"ms":    elapsed.Milliseconds(),
				})
			case <-timer.C:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGatewayTimeout)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
					"acked":  false,
					"id":     ackID,
					"reason": "timeout",
					"ms":     ackWait.Milliseconds(),
				})
			case <-req.Context().Done():
				return
			}
		}

		// Mirror the outgoing message into the SSE stream so headless
		// listeners (skywire-cli skychat listen) see a complete
		// transcript without having to scrape send invocations and
		// merge by timestamp. The TUI's bidirectional view already
		// renders both directions natively; this brings parity to the
		// listen-on-the-CLI use case.
		//
		// IMPORTANT: dir="out" means "WriteFrame returned without
		// error" — i.e. the framed payload was handed to the
		// underlying skywire conn. It does NOT mean the peer's
		// chat-app received or processed the message. Peer-app
		// receipt-ack is a deferred protocol feature (msg-id +
		// chat-ack envelope frame type); consumers should not treat
		// dir:out as delivery confirmation.
		//
		// The "sender" field carries the RECIPIENT'S PK here (per-peer
		// thread routing on the consumer's side stays keyed by the
		// remote PK regardless of direction). dir distinguishes
		// directionality (string, not bool, for extensibility — future
		// relay/group flows can emit "relay" / "group-in" /
		// "group-out" without a wire-schema bump).
		// "to" is set on dir:out events so consumers can correlate
		// outgoing mirrors back to a specific send. dir:in events
		// don't need "to" (we're always the recipient).
		// "from" stays as the visor's own PK so consumers can route
		// by per-peer thread regardless of direction.
		var myPK string
		if appCl != nil {
			myPK = appCl.Config().VisorPK.Hex()
		}
		// Use the ack id when --wait was used, so consumers correlate
		// the outgoing mirror to a specific send by id.
		mirrorID := ackID
		if mirrorID == "" {
			mirrorID = newEventID()
		}
		mirrorMsg, mErr := json.Marshal(map[string]interface{}{
			"sender":  myPK,
			"to":      pk.Hex(),
			"message": data.Message,
			"network": string(netType),
			"dir":     "out",
			"id":      mirrorID,
			"len":     len(data.Message),
		})
		if mErr == nil {
			hub.broadcast(string(mirrorMsg))
		}

		counterMu.Lock()
		outboundMsgCount++
		lastSendAt = time.Now().UTC()
		counterMu.Unlock()
	}
}

// sseKeepaliveInterval is how often sseHandler writes a `: ping`
// comment line to keep the connection warm. SSE per the spec ignores
// any line starting with `:` so this is invisible to clients. The
// interval is well below the http.Server.WriteTimeout we set on the
// listener so write activity never goes idle long enough to trigger
// a deadline-based close — and any reverse proxy in front of skychat
// (Caddy/nginx) also sees a steady stream and won't time out.
const sseKeepaliveInterval = 15 * time.Second

func sseHandler(w http.ResponseWriter, req *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusBadRequest)
		return
	}

	// Disable WriteTimeout for this request. Long-lived SSE streams
	// are fundamentally incompatible with a per-conn write deadline
	// — an idle subscriber would see the server tear down the conn
	// after WriteTimeout and the client surfaces it as `unexpected
	// EOF`. Clearing the deadline keeps the conn open until either
	// the client closes it or req.Context().Done() fires on shutdown.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		// Older Go versions or wrapped writers may not support it;
		// we can still serve — just slightly more aggressive close
		// behavior if the operator runs an old server. Debug-log
		// rather than failing the connection.
		chatLog.Debugf("SSE SetWriteDeadline: %v", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering

	ch, unsubscribe := hub.subscribe()
	defer unsubscribe()

	// Seed the new SSE client with recent history if persistence is enabled.
	if historyStore != nil && persistSeedCount > 0 {
		recent, err := historyStore.ListRecent(persistSeedCount)
		if err != nil {
			chatLog.Debugf("SSE seed list failed: %v", err)
		} else {
			for _, m := range recent {
				sender := m.From
				if m.Outgoing {
					sender = "self"
				}
				b, _ := json.Marshal(map[string]string{ //nolint:errcheck,gosec
					"sender":  sender,
					"message": m.Text,
					"peer":    m.Peer,
					"ts":      m.Timestamp.Format(time.RFC3339),
					"history": "true",
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b) //nolint:errcheck,gosec
			}
			f.Flush()
		}
	}

	// Emit an initial keepalive comment immediately so the client
	// gets confirmation the stream is open even before any real
	// message arrives. Browsers (EventSource) and our CLI listen
	// both treat lines beginning with `:` as no-ops.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		chatLog.Debugf("SSE initial keepalive write failed: %v", err)
		return
	}
	f.Flush()

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				// Client gone (write to a closed conn) — exit so the
				// hub deregisters this subscriber and stops buffering
				// messages it can't deliver.
				chatLog.Debugf("SSE write failed, dropping subscriber: %v", err)
				return
			}
			f.Flush()

		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				chatLog.Debugf("SSE keepalive write failed: %v", err)
				return
			}
			f.Flush()

		case <-req.Context().Done():
			chatLog.Debug("SSE connection was closed.")
			return
		}
	}
}

// historyHandler returns JSON history. Query params:
//
//	peer=<pk>    — filter to a specific peer
//	limit=<int>  — max messages to return (default 100, max 1000)
func historyHandler(w http.ResponseWriter, req *http.Request) {
	if historyStore == nil {
		http.Error(w, "persistence not enabled", http.StatusServiceUnavailable)
		return
	}
	peer := req.URL.Query().Get("peer")
	limit := 100
	if v := req.URL.Query().Get("limit"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var msgs []history.Message
	var err error
	if peer != "" {
		msgs, err = historyStore.ListByPeer(peer, limit)
	} else {
		msgs, err = historyStore.ListRecent(limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs) //nolint:errcheck,gosec
}

func historyPeersHandler(w http.ResponseWriter, _ *http.Request) {
	if historyStore == nil {
		http.Error(w, "persistence not enabled", http.StatusServiceUnavailable)
		return
	}
	peers, err := historyStore.Peers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(peers) //nolint:errcheck,gosec
}

// statusHandler returns a snapshot of the chat-app's runtime health
// for operator probes. Replaces the chain of `docker exec + ss -tlnp
// + curl /sse` an operator would otherwise need to verify the app
// is up. JSON shape is stable — added fields are backward-compatible.
//
// Fields:
//
//	visor_pk             current PK the app is bound under
//	sse_subscribers      live SSE listener count
//	active_peer_conns    live peer chat conns (CHAT-APP LAYER —
//	                     framed connections the app holds; NOT a
//	                     dmsg session count. After a visor restart
//	                     this starts at 0 and only grows when this
//	                     app initiates an outbound DM or accepts an
//	                     inbound one. Underlying dmsg may be fully
//	                     reachable while this reads 0 — that's not
//	                     a network problem, just a count of how
//	                     many active chat sessions this app is
//	                     holding open.)
//	peers                PKs of the active_peer_conns. Same
//	                     chat-app-layer caveat — these are NOT the
//	                     visor's dmsg session list.
//	persistence_enabled  history store is initialized
//	pairing_enabled      pair-control sub-protocol is on
//	frame_proto_version  on-the-wire chat-frame version (diagnose
//	                     staggered-deploy version skew before it
//	                     manifests as confusing wire failures)
//	schema_version       listen-output JSON event-shape version
//	app_uptime_sec       since the app started
//	inbound_msg_count    chat frames successfully decoded since start
//	outbound_msg_count   chat frames successfully written since start
//	inbound_drop_count   ReadFrame errors since start; if this climbs
//	                     while inbound_msg_count is flat, the receive
//	                     path is broken
//	outbound_fail_count  /message requests that gave up after the
//	                     in-handler redial+retry exhausted itself —
//	                     these are real data-loss events visible to
//	                     the caller as HTTP 400.
//	outbound_retry_count /message requests where the first write
//	                     errored on a cached conn and the in-handler
//	                     redial succeeded. Healthy steady state is
//	                     ~0; non-zero means peers' transports are
//	                     flapping but we masked it within the request.
//	sse_drop_count       messages the SSE hub dropped to listeners
//	                     whose per-client buffer was full at
//	                     broadcast time. Each drop is a message one
//	                     listener missed.
//	last_rx_ts           last successful inbound (RFC3339 / "" if none)
//	last_send_ts         last successful outbound (RFC3339 / "" if none)
func statusHandler(w http.ResponseWriter, _ *http.Request) {
	connsMu.Lock()
	connCount := len(conns)
	peers := make([]string, 0, connCount)
	for pk := range conns {
		peers = append(peers, pk.Hex())
	}
	connsMu.Unlock()

	var subscriberCount int
	if hub != nil {
		subscriberCount = hub.clientCount()
	}

	var visorPK string
	var visorPKErr string
	if appCl != nil {
		visorPK = appCl.Config().VisorPK.Hex()
	} else {
		visorPKErr = "app client not initialized"
	}

	counterMu.Lock()
	inMsgs := inboundMsgCount
	outMsgs := outboundMsgCount
	inDrops := inboundDropCount
	outFails := outboundFailCount
	outRetries := outboundRetryCount
	outFallbacks := outboundFallbackCount
	sseDrops := sseDropCount
	lastRx := lastRxAt
	lastSend := lastSendAt
	counterMu.Unlock()

	rxStr := ""
	if !lastRx.IsZero() {
		rxStr = lastRx.UTC().Format(time.RFC3339Nano)
	}
	sendStr := ""
	if !lastSend.IsZero() {
		sendStr = lastSend.UTC().Format(time.RFC3339Nano)
	}

	status := map[string]interface{}{
		"visor_pk":                visorPK,
		"sse_subscribers":         subscriberCount,
		"active_peer_conns":       connCount,
		"peers":                   peers,
		"persistence_enabled":     historyStore != nil,
		"pairing_enabled":         pairEnable,
		"frame_proto_version":     frameProtoVersion,
		"schema_version":          schemaVersion,
		"app_uptime_sec":          int64(time.Since(startedAt).Seconds()),
		"inbound_msg_count":       inMsgs,
		"outbound_msg_count":      outMsgs,
		"inbound_drop_count":      inDrops,
		"outbound_fail_count":     outFails,
		"outbound_retry_count":    outRetries,
		"outbound_fallback_count": outFallbacks,
		"sse_drop_count":          sseDrops,
		"last_rx_ts":              rxStr,
		"last_send_ts":            sendStr,
	}
	if visorPKErr != "" {
		status["error"] = visorPKErr
	}
	// Always surface a result for the groups[] introspection path
	// even when it fails. Previously a nil pairRPC or a GroupList RPC
	// failure was returned as a silent nil and the 'groups' key was
	// suppressed entirely — making it impossible for an operator to
	// distinguish "this visor has no groups" from "the introspection
	// path is broken". Now we always set 'groups' (an array, possibly
	// empty) AND, on the failure paths, set 'groups_error' with a
	// short reason string so the failure mode is greppable.
	groups, groupsErr := collectGroupHealth()
	status["groups"] = groups
	if groupsErr != "" {
		status["groups_error"] = groupsErr
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status) //nolint:errcheck,gosec
}

// groupHealth is the per-group health summary surfaced by /status.
// lag_seconds is a pointer so JSON encoding emits explicit null when
// the group has never seen a message (last_message_at is the zero
// time). Operators can then treat "lag_seconds > 600" as a stale-feed
// alarm without false-positive on brand-new empty groups.
type groupHealth struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Role            string    `json:"role"`
	Status          string    `json:"status"`
	MembersCount    int       `json:"members_count"`
	LastMessageAt   time.Time `json:"last_message_at,omitempty"`
	LagSeconds      *int64    `json:"lag_seconds"`
	SubscriberAlive bool      `json:"subscriber_alive"`
	// SubDropCount is the inbox→stream fan-out drop tally surfaced
	// from the visor's group inbox. See visor.GroupInfo.SubDropCount
	// for the full semantics. Repeated across every group entry in
	// this list because the underlying counter is inbox-wide, not
	// per-group — a busy stream that backpressures up to the
	// channel-full default branch drops messages destined for every
	// group, but operators looking at one group's /status entry
	// shouldn't have to know that to find the number.
	SubDropCount uint64 `json:"sub_drop_count"`
}

// collectGroupHealth queries the visor's GroupList RPC and renders
// the per-group health entries for /status. Returns nil if the
// pair-RPC channel isn't wired (grouping then can't be inspected
// from the chat-app process). Returns an empty slice if the visor
// has no groups (vs. nil = "unknown / not introspectable") so
// consumers can distinguish "no groups configured" from "grouping
// unreachable".
//
// Why route through the visor RPC: the chat-app process doesn't own
// the group.Manager — the visor does. The chat-app already opens a
// pair-RPC channel (when --pair-enable is set, which is the default
// for any setup that has grouping enabled at all). This reuses that
// channel rather than introducing a new IPC dependency.
// collectGroupHealth returns the per-group health summary for every
// joined group on this visor, plus a short error-reason string when
// the introspection path failed. The caller always renders a 'groups'
// array (possibly empty) so an operator never sees the field silently
// missing; the 'groups_error' field appears alongside whenever the
// returned reason is non-empty, making the failure mode visible.
//
// Two failure paths surface as distinct reason strings:
//
//   - pairRPC == nil  → "pair-rpc-disabled" (operator turned off
//     pair mode, so the chat-app can't reach the visor for group
//     introspection; not necessarily a bug).
//   - GroupList RPC errored → "rpc-error: <truncated err>" (the
//     visor's group manager rejected or failed the call — usually
//     means group support is disabled in the visor config, OR the
//     RPC client has a transient connection issue).
func collectGroupHealth() ([]groupHealth, string) {
	if !pairRPCAlive() {
		return []groupHealth{}, "pair-rpc-disabled"
	}
	var infos []visor.GroupInfo
	err := pairRPCCall("GroupList", func(c visor.API) error {
		out, e := c.GroupList()
		infos = out
		return e
	})
	if err != nil {
		chatLog.Debugf("status: GroupList RPC failed: %v", err)
		// Truncate the err so a long upstream chain doesn't bloat
		// /status responses or wrap unprintable bytes through HTTP.
		es := err.Error()
		if len(es) > 200 {
			es = es[:200] + "…"
		}
		return []groupHealth{}, "rpc-error: " + es
	}
	out := make([]groupHealth, 0, len(infos))
	now := time.Now().UTC()
	for _, g := range infos {
		gh := groupHealth{
			ID:              g.ID,
			Name:            g.Name,
			Role:            string(g.Role),
			Status:          string(g.Status),
			MembersCount:    len(g.Members),
			LastMessageAt:   g.LastMessageAt,
			SubscriberAlive: g.SubscriberAlive,
			SubDropCount:    g.SubDropCount,
		}
		if !g.LastMessageAt.IsZero() {
			lag := int64(now.Sub(g.LastMessageAt).Seconds())
			if lag < 0 {
				lag = 0
			}
			gh.LagSeconds = &lag
		}
		out = append(out, gh)
	}
	return out, ""
}

// persistMessage stores a message in the history backend if persistence is
// enabled. Errors are logged at debug level; ephemeral delivery is never
// blocked by persistence failure.
func persistMessage(msg history.Message) {
	if historyStore == nil {
		return
	}
	if err := historyStore.Append(msg); err != nil {
		switch {
		case errors.Is(err, history.ErrRateLimited),
			errors.Is(err, history.ErrTooLarge),
			errors.Is(err, history.ErrStorageFull),
			errors.Is(err, history.ErrNotWhitelisted):
			chatLog.Debugf("history: dropped %s (%v)", msg.Peer, err)
		default:
			appLog("history: backend error: %v", err)
		}
	}
}

// openHistoryStore constructs the bolt history store from CLI flags.
func openHistoryStore() error {
	dbPath := persistDBPath
	if dbPath == "" {
		// In --standalone mode appCl is nil and ProcWorkDir is
		// unavailable; fall back to skyenv.LocalPath which is the
		// same default the visor-launcher would have set anyway.
		var workDir string
		if appCl != nil {
			workDir = appCl.Config().ProcWorkDir
		}
		if workDir == "" {
			workDir = skyenv.LocalPath
		}
		dbPath = filepath.Join(workDir, "skychat-history.db")
	}

	limits := history.Limits{
		MaxMessageSize:    persistMaxMsgSize,
		PerPeerRatePerMin: persistPerPeerRate,
		PerPeerCap:        persistPerPeerCap,
		TotalCapBytes:     int64(persistTotalCapMB) * 1024 * 1024,
		TTL:               time.Duration(persistTTLDays) * 24 * time.Hour,
	}
	if persistWhitelistFile != "" {
		wl, err := loadWhitelist(persistWhitelistFile)
		if err != nil {
			return fmt.Errorf("load whitelist: %w", err)
		}
		limits.WhitelistOnly = true
		limits.Whitelist = wl
	}

	s, err := history.NewBoltStore(dbPath, limits)
	if err != nil {
		return err
	}
	historyStore = s
	appLog("Persistence enabled: db=%s cap=%dMB per-peer=%d ttl=%dd whitelist=%v",
		dbPath, persistTotalCapMB, persistPerPeerCap, persistTTLDays, limits.WhitelistOnly)
	return nil
}

// loadWhitelist reads a file with one peer PK hex per line (ignoring blanks
// and lines starting with #).
func loadWhitelist(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	wl := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		wl[line] = true
	}
	return wl, nil
}

func getFileSystem() http.FileSystem {
	fsys, err := fs.Sub(embededFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(fsys)
}

func handleIPCSignal(client *ipc.Client) {
	time.Sleep(5 * time.Second)
	if client == nil {
		appLog("Unable to create IPC Client: server is non-existent")
		return
	}
	for {
		m, err := client.Read()
		if err != nil {
			appLog("%s IPC received error: %v", skyenv.SkychatName, err)
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				appLog("Stopping %s via IPC", skyenv.SkychatName)
				break
			}
		}

	}
	client.Close()
}
