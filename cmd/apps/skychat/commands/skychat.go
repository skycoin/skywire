// Package commands cmd/apps/skychat/commands/skychat.go c4-app-chat
package commands

import (
	"context"
	cryptoRand "crypto/rand"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor"
)

var r = netutil.NewRetrier(nil, 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

// skychat's peer-to-peer wire format — length-prefixed frames plus the
// chat-msg/chat-ack envelope — now lives in one place: pkg/skychat/message.
// framedConn is a thin alias over message.Conn so the many call sites in this
// file keep their local name; newFramedConn wraps a raw appnet conn with framing
// and a write mutex (needed because the /message handler and the pair-control
// sender can race to write the same conn — interleaving one frame's length prefix
// with another's payload would desync the receiver permanently).
//
// History: pre-2026-05-12 the protocol was "one Write = one Read" (a raw byte
// slice per message). That held on dmsg (noise-framed) but broke on skynet
// routes, where a VStream can split one Write across several Reads at arbitrary
// boundaries — a 600-byte message arrived as two chat entries. The length-prefixed
// frame fixed that; old unframed binaries can't talk to framed ones.
var (
	addr     string
	portless bool
	appCl    *app.Client
	appLog   func(format string, args ...interface{}) // App logger function
	// chatLog is the package-level logrus logger every code path can
	// reach without going through appCl. In visor-launched mode it
	// proxies through appCl.Log(); in --standalone mode appCl is nil
	// and chatLog holds the stderr logrus instance directly. HTTP
	// handlers + SSE pumps + accept loops use chatLog to avoid the
	// nil-deref crash class on standalone.
	chatLog   logrus.FieldLogger
	hub       *sseHub        // SSE broadcast registry; see sse.go-like helpers below
	chatCtrl  *dm.Controller // shared 1:1 DM core (pkg/skychat/dm): conns, read loop, envelopes, send
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
	// counterMu guards the SSE-hub drop counter below. The DM message
	// counters (in/out, drops, retries, fallbacks, last rx/tx) now live in
	// the dm.Controller and are read via chatCtrl.Stats() in /status.
	counterMu sync.Mutex
	startedAt = time.Now()

	// sseDropCount counts messages the SSE hub dropped because a
	// subscriber's per-client buffer was full at broadcast time.
	// Surfaced in /status so operators can tell "my listener missed N
	// msgs" without log-scraping.
	sseDropCount uint64
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

	// Structured event stream backing GET /events (cli skychat events).
	// Each published event gets a monotonic seq; the ring keeps the last
	// eventRingSize events (also bounded by eventTTL on replay) so a
	// reconnecting consumer resumes from its last seq via ?since=. Separate
	// from the legacy string replay above so /sse stays byte-for-byte
	// unchanged.
	seq        uint64
	events     []chatEvent
	eventsHead int
	eventsLen  int
	eventSubs  map[*eventSub]struct{}
}

// chatEvent is the structured form of a chat event, streamed as NDJSON by
// GET /events. Agreed schema (Beta+Gamma consensus): seq cursor + id-keyed
// dedupe, channel taxonomy, in/out direction.
type chatEvent struct {
	Seq       uint64    `json:"seq"`
	ID        string    `json:"id"`
	TS        time.Time `json:"ts"`
	Channel   string    `json:"channel"`   // dm|group|pair|system
	Transport string    `json:"transport"` // skynet|dmsg|cxo|tcp-direct
	Dir       string    `json:"dir"`       // in|out
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	GroupID   string    `json:"group_id,omitempty"`
	Text      string    `json:"text"`
	ReplyToID string    `json:"reply_to_id,omitempty"`
	Len       int       `json:"len"`

	// File attachment fields (Telegram-style: a file is a message). Set only on
	// file events; empty on plain chat. FileID is the transfer id; FileName /
	// FileSize describe the file; FilePath is the LOCAL path the receiver saved
	// it to (populated on "in" events once the transfer verifies, empty on the
	// sender's "out" event). FileStatus is "sent"|"received"|"failed".
	FileID     string `json:"file_id,omitempty"`
	FileName   string `json:"file_name,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	FilePath   string `json:"file_path,omitempty"`
	FileStatus string `json:"file_status,omitempty"`
}

// eventSub is a /events subscriber: a buffered channel plus the set of
// channels it wants (nil = the default dm+group+pair set).
type eventSub struct {
	ch       chan chatEvent
	channels map[string]bool
}

const (
	// eventRingSize bounds the structured-event replay ring.
	eventRingSize = 10000
	// eventTTL bounds replay age — events older than this are not replayed
	// even if still in the ring (the 24h window from the agreed design).
	eventTTL = 24 * time.Hour
	// eventSubBufSize is the per-/events-client buffer depth.
	eventSubBufSize = 256
)

// Channel taxonomy.
const (
	channelDM     = "dm"
	channelGroup  = "group"
	channelPair   = "pair"
	channelSystem = "system"
)

// defaultEventChannels is what `--channel all` (or no filter) resolves to:
// dm+group+pair. "system" is opt-in only.
func defaultEventChannels() map[string]bool {
	return map[string]bool{channelDM: true, channelGroup: true, channelPair: true}
}

func newSSEHub() *sseHub {
	return &sseHub{
		clients:   make(map[chan string]struct{}),
		replay:    make([]string, sseReplayBufSize),
		events:    make([]chatEvent, eventRingSize),
		eventSubs: make(map[*eventSub]struct{}),
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

// recordEvent assigns the monotonic seq + timestamp, stores the event in the
// structured ring, and fans it out to /events subscribers (channel-filtered).
// It does NOT touch the legacy /sse stream — callers that also want /sse
// either use publishEvent (chat messages) or keep their own hub.broadcast call
// (pairing, which has its own /sse control-event shape).
func (h *sseHub) recordEvent(ev chatEvent) {
	if ev.Len == 0 {
		ev.Len = len(ev.Text)
	}
	if ev.Channel == "" {
		ev.Channel = channelDM
	}

	h.mu.Lock()
	h.seq++
	ev.Seq = h.seq
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	h.events[h.eventsHead] = ev
	h.eventsHead = (h.eventsHead + 1) % len(h.events)
	if h.eventsLen < len(h.events) {
		h.eventsLen++
	}
	for sub := range h.eventSubs {
		if !sub.channels[ev.Channel] {
			continue
		}
		select {
		case sub.ch <- ev:
		default: // slow consumer — recoverable via ?since= on reconnect
		}
	}
	h.mu.Unlock()
}

// publishEvent records a chat-message event for /events AND renders the legacy
// SSE JSON into the unchanged /sse path — one call reaches both surfaces in
// order. Use for chat messages (dm/group); pairing uses recordEvent + its own
// broadcast.
func (h *sseHub) publishEvent(ev chatEvent) {
	if ev.Len == 0 {
		ev.Len = len(ev.Text)
	}
	if ev.Channel == "" {
		ev.Channel = channelDM
	}
	h.recordEvent(ev)
	h.broadcast(renderLegacySSE(ev))
}

// renderLegacySSE renders a chatEvent into the historical /sse JSON shape
// {sender, message, network, dir, id, len, [to]} so existing /sse consumers
// (incl. `cli skychat listen`) are unaffected by the richer /events schema.
func renderLegacySSE(ev chatEvent) string {
	m := map[string]interface{}{
		"sender":  ev.From,
		"message": ev.Text,
		"network": ev.Transport,
		"dir":     ev.Dir,
		"id":      ev.ID,
		"len":     ev.Len,
	}
	if ev.To != "" {
		m["to"] = ev.To
	}
	if ev.ReplyToID != "" {
		// Surface the quoted-reply target on the legacy /sse shape too, so the
		// bundled skychat SPA (which consumes /sse) can thread replies without
		// switching to /events.
		m["reply_to_id"] = ev.ReplyToID
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// subscribeEvents registers a /events subscriber that wants the given channels
// (nil = the default dm+group+pair set) and replays buffered events with
// seq > since (within eventTTL) before live-following. Returns the channel and
// an unsubscribe func the caller MUST invoke.
func (h *sseHub) subscribeEvents(channels map[string]bool, since uint64) (<-chan chatEvent, func()) {
	// Resolve nil ("default") to the dm+group+pair set so system stays opt-in
	// and the fan-out/replay filters can assume a concrete set.
	if channels == nil {
		channels = defaultEventChannels()
	}
	sub := &eventSub{ch: make(chan chatEvent, eventSubBufSize), channels: channels}
	h.mu.Lock()
	if h.eventsLen > 0 {
		start := (h.eventsHead - h.eventsLen + len(h.events)) % len(h.events)
	replay:
		for i := 0; i < h.eventsLen; i++ {
			ev := h.events[(start+i)%len(h.events)]
			if ev.Seq <= since {
				continue
			}
			if !channels[ev.Channel] {
				continue
			}
			if time.Since(ev.TS) > eventTTL {
				continue
			}
			select {
			case sub.ch <- ev:
			default:
				break replay // buffer full; remainder recoverable via ?since=
			}
		}
	}
	h.eventSubs[sub] = struct{}{}
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		if _, ok := h.eventSubs[sub]; ok {
			delete(h.eventSubs, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}
}

// parseEventChannels turns a comma-separated channel list into a set. Empty or
// "all" → the default dm+group+pair set. "all" combined with extras (e.g.
// "all,system") expands "all" then unions the extras. Unknown tokens are
// ignored. Returns nil only for the pure-default case so callers can treat nil
// as "default set" cheaply.
func parseEventChannels(csv string) map[string]bool {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	set := make(map[string]bool)
	onlyAll := true
	for _, tok := range strings.Split(csv, ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "", "all":
			for c := range defaultEventChannels() {
				set[c] = true
			}
		case channelDM:
			set[channelDM] = true
			onlyAll = false
		case channelGroup:
			set[channelGroup] = true
			onlyAll = false
		case channelPair:
			set[channelPair] = true
			onlyAll = false
		case channelSystem:
			set[channelSystem] = true
			onlyAll = false
		}
	}
	if len(set) == 0 || onlyAll {
		return nil // pure default
	}
	return set
}

func init() {
	launcher.RegisterApp("skychat", RunSkychat)
	RootCmd.Flags().StringVar(&addr, "addr", ":8001", "address to bind (default: localhost-only); use \"*:PORT\" to bind on all interfaces")
	RootCmd.Flags().BoolVar(&portless, "portless", false, "portless-internal: don't bind a TCP port; publish the HTTP surface in-process for the visor's control surface to serve (default for internal-app deployments)")
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
		fs.BoolVar(&portless, "portless", false, "portless-internal: no TCP port; publish HTTP surface in-process")
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

	// The 1:1 DM path — per-peer framed conns, listen/accept + read loops, the
	// chat-msg/chat-ack/quoted-reply envelope handling, and the send path
	// (ack-wait, stale-conn redial-retry, cross-network fallback) — is the
	// shared pkg/skychat/dm.Controller, the same core the wasm visor uses.
	// Persistence goes through historyStore (whitelist/rate/cap guardrails
	// still apply); every surfaced message (inbound + the outbound mirror) is
	// published to the SSE hub via OnEvent; pair-control frames are intercepted
	// by PreHandleFrame; dials keep the app's retrier. In --standalone (appCl
	// nil) there's no transport to dial or listen on — the controller is still
	// created (so TCP-direct conns can be Serve'd into it), just not Started.
	var nets []appnet.Type
	if useSkynet {
		nets = append(nets, appnet.TypeSkynet)
	}
	if useDmsg {
		nets = append(nets, appnet.TypeDmsg)
	}
	var dmClient dm.Client
	if appCl != nil {
		dmClient = appCl
	}
	chatCtrl = dm.New(dm.Config{
		Client:    dmClient,
		Store:     historyStore,
		Networks:  nets,
		Port:      port,
		OnEvent:   onChatEvent,
		DialRetry: func(dctx context.Context, fn func() error) error { return r.Do(dctx, fn) },
		PreHandleFrame: func(peer cipher.PubKey, payload []byte) bool {
			return handlePairControlFrame(context.Background(), peer, payload)
		},
		Log: func(f string, a ...interface{}) { appLog(f, a...) },
	})
	if len(nets) > 0 && !standalone && appCl != nil {
		if err := chatCtrl.Start(ctx); err != nil {
			appLog("skychat: dm controller start: %v", err)
		}
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
	// File transfer: listens on skyenv.SkychatFilePort over the enabled networks
	// so peers can send us files (Telegram-style). Nil-effect if appCl is nil.
	// See filexfer.go.
	startFileXfer(ctx)
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
	mux.HandleFunc("/events", requireAuthFunc(eventsHandler))
	mux.HandleFunc("/history", requireAuthFunc(historyHandler))
	mux.HandleFunc("/history/peers", requireAuthFunc(historyPeersHandler))
	mux.HandleFunc("/status", requireAuthFunc(statusHandler))
	mux.HandleFunc("/send-file", requireAuthFunc(sendFileHandler(ctx)))
	mux.HandleFunc("/files/", requireAuthFunc(downloadFileHandler))
	registerPairHTTPHandlers(ctx, mux)

	// Portless-internal mode: no TCP port. Publish the mux to the visor's
	// in-process HTTP-handler registry so the hypervisor's control surface
	// (Visor.SkychatProxy) serves it directly via ServeHTTP — no loopback dial,
	// and no http.Server WriteTimeout to strangle the SSE stream. The dmsg:1 /
	// skynet:1 mesh listeners (the actual chat transport, started above) run
	// regardless. This is the native twin of the wasm visor's in-process
	// skychat, and the default for an internal-app deployment.
	if portless {
		launcher.RegisterHTTPHandler(skyenv.SkychatName, mux)
		defer launcher.RegisterHTTPHandler(skyenv.SkychatName, nil)
		appLog("Serving skychat in-process (portless) via the visor control surface")
		appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
		<-ctx.Done()
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		return nil
	}

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
			// ReplyTo, if set, is the id of a prior message this send
			// quotes (a threaded reply). It forces a chat-msg envelope
			// (see below) carrying ReplyTo so the RECIPIENT surfaces the
			// thread — the receive-side wiring already reads env.ReplyTo
			// (sendack.go). Empty keeps a default send byte-identical.
			ReplyTo string `json:"reply_to,omitempty"`
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
			_ = path
			hub.publishEvent(chatEvent{
				ID:        newEventID(),
				Channel:   channelGroup,
				Transport: "cxo",
				Dir:       "out",
				From:      myPK.Hex(),
				To:        data.Recipient,
				GroupID:   cxoGroup,
				Text:      data.Message,
			})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true,"network":"cxo"}`)) //nolint:errcheck,gosec
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

		// Deliver through the shared DM controller (pkg/skychat/dm): it (re)uses a
		// cached conn, writes with a deadline, redial-retries a stale cached conn,
		// falls back to the alternate network, persists, and surfaces the outbound
		// mirror via OnEvent → publishEvent. For a wait_ms send it wraps a chat-msg
		// envelope and blocks on the peer's chat-ack.
		opt := dm.SendOpts{ReplyTo: data.ReplyTo}
		if data.WaitMS > 0 {
			opt.WaitAck = clampAckWait(time.Duration(data.WaitMS) * time.Millisecond)
		}
		res, sErr := chatCtrl.Send(ctx, pk, netType, data.Message, opt)
		if sErr != nil {
			http.Error(w, sErr.Error(), http.StatusBadRequest)
			return
		}
		if data.WaitMS > 0 {
			w.Header().Set("Content-Type", "application/json")
			if res.Acked {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
					"acked": true, "id": res.ID, "ms": res.Elapsed.Milliseconds(),
				})
			} else {
				w.WriteHeader(http.StatusGatewayTimeout)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
					"acked": false, "id": res.ID, "reason": "timeout", "ms": opt.WaitAck.Milliseconds(),
				})
			}
		}
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

// eventsHandler is the structured event stream backing `cli skychat events`.
// It is SSE (text/event-stream) with one NDJSON chatEvent per `data:` line.
//
//	GET /events?channel=<csv>&since=<seq>
//
// channel: comma-separated dm|group|pair|system|all (default/empty/all =
//
//	dm+group+pair; "system" is opt-in). since: resume after this seq (replay
//	buffered events with seq>since within the 24h window, then live-follow).
func eventsHandler(w http.ResponseWriter, req *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusBadRequest)
		return
	}
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		chatLog.Debugf("events SetWriteDeadline: %v", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Accel-Buffering", "no")

	channels := parseEventChannels(req.URL.Query().Get("channel"))
	var since uint64
	if v := req.URL.Query().Get("since"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &since); err != nil {
			http.Error(w, "invalid since", http.StatusBadRequest)
			return
		}
	}

	ch, unsubscribe := hub.subscribeEvents(channels, since)
	defer unsubscribe()

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		chatLog.Debugf("events initial keepalive write failed: %v", err)
		return
	}
	f.Flush()

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				chatLog.Debugf("events write failed, dropping subscriber: %v", err)
				return
			}
			f.Flush()

		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				chatLog.Debugf("events keepalive write failed: %v", err)
				return
			}
			f.Flush()

		case <-req.Context().Done():
			chatLog.Debug("events connection was closed.")
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
	var connCount int
	var peers []string
	if chatCtrl != nil {
		connCount = chatCtrl.Conns()
		peers = chatCtrl.Peers()
	}
	if peers == nil {
		peers = []string{}
	}

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

	var st dm.Stats
	if chatCtrl != nil {
		st = chatCtrl.Stats()
	}
	counterMu.Lock()
	sseDrops := sseDropCount
	counterMu.Unlock()

	rxStr := ""
	if !st.LastRxAt.IsZero() {
		rxStr = st.LastRxAt.UTC().Format(time.RFC3339Nano)
	}
	sendStr := ""
	if !st.LastTxAt.IsZero() {
		sendStr = st.LastTxAt.UTC().Format(time.RFC3339Nano)
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
		"inbound_msg_count":       st.InboundMsgs,
		"outbound_msg_count":      st.OutboundMsgs,
		"inbound_drop_count":      st.InboundDrops,
		"outbound_fail_count":     st.OutboundFails,
		"outbound_retry_count":    st.OutboundRetry,
		"outbound_fallback_count": st.OutboundFallbk,
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
// onChatEvent bridges the shared DM controller's surfaced messages (inbound +
// the outbound mirror) into the SSE hub. Persistence and counters live in the
// controller now, so this only fans the event out to /sse and /events. The
// event id is the message's own envelope id when present (so a reply's
// reply_to_id resolves to it), else a fresh id; an outbound mirror carries the
// sender=self, to=peer shape the UI expects.
func onChatEvent(ev dm.Event) {
	id := ev.ID
	if id == "" {
		id = newEventID()
	}
	from, to := ev.Peer, ""
	if ev.Dir == "out" {
		from = ""
		if appCl != nil {
			from = appCl.Config().VisorPK.Hex()
		}
		to = ev.Peer
	}
	hub.publishEvent(chatEvent{
		ID:        id,
		Channel:   channelDM,
		Transport: ev.Network,
		Dir:       ev.Dir,
		From:      from,
		To:        to,
		Text:      ev.Text,
		ReplyToID: ev.ReplyTo,
		Len:       len(ev.Text),
	})
}

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
