//go:build js && wasm

// Package main cmd/wasm-visor/skychat_js.go c3-vis-wasm
//
// A native visor runs skychat as a subprocess that serves an HTTP UI and chats
// over routes. A browser can't fork a subprocess, so this is a minimal in-process
// skychat (appcommon.RunModeInternal): it speaks skychat's wire protocol —
// length-prefixed frames (4-byte big-endian length + payload) over an app.Client
// route on the well-known skychat port (dmsg:1) — so it interoperates with a
// native visor's skychat. The page sends/reads messages via skywireVisor.skychat*
// instead of an HTTP UI.
//
// Scope: the core messaging path (listen, accept, read/write framed messages).
// The native app's HTTP UI, BoltDB persistence, CXO pairing, and TCP-direct mode
// are intentionally omitted (none fit a browser tab). Default frames are plain
// UTF-8 text — the format native skychat sends without --wait — and the native
// "chat-msg" JSON envelope is also decoded, so a wasm↔native chat works either way.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// skychatPort is skychat's well-known routing port: native skychat dials peers
// at appnet.Addr{...Port: 1}, so the in-process app must listen there to be
// reachable, and dials peers there in turn.
const skychatPort = 1

// chatMsg is one buffered message surfaced to the page (skychatMessages()).
type chatMsg struct {
	ID   string `json:"id"`   // stable message id (envelope id); addressable for replies
	From string `json:"from"` // peer PK hex (the other end)
	Text string `json:"text"`
	TS   int64  `json:"ts"`  // unix milliseconds
	Out  bool   `json:"out"` // true = we sent it, false = received
	// ReplyTo, when set, is the ID of the message this one quotes (threaded
	// reply). Carried on the wire in message.Envelope.ReplyTo — shared with the
	// native app, so a reply sent from either side shows its quote on the other.
	ReplyTo string `json:"reply_to,omitempty"`

	// File attachment fields (Telegram-style: a file is a message). Set only on
	// file events; empty on plain chat. On a received file (Out=false) FileID
	// keys the in-memory blob the page fetches via skychatFile(id) to download.
	IsFile   bool   `json:"is_file,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
	FileOK   bool   `json:"file_ok,omitempty"` // transfer verified
}

// Peer conns use the shared skychat wire codec: message.Conn carries the
// length-prefixed framing plus the write mutex a bidirectional skychat conn needs
// (sendChat writes outbound messages while readChatConn writes chat-ack replies on
// the same conn — the two must be serialized or their frames interleave). The
// chat-ack envelope and frame codec live in pkg/skychat/message, shared with the
// native app.

var (
	chatMu        sync.Mutex
	chatStoreOnce sync.Once
	chatStore     *history.MemStore // shared skychat history backend (pkg/skychat/history)
	// fileMeta holds the wasm-only file-transfer fields of file entries, keyed
	// by the entry's message ID — history.Message doesn't carry them, so we
	// re-hydrate them when projecting a stored record back to a chatMsg. File
	// entries are rare (a browser session sends/receives few files), so the map
	// isn't evicted in lockstep with the store's per-peer cap.
	fileMeta   = map[string]chatMsg{}
	chatClient *app.Client
	chatConns  = map[string]*message.Conn{} // cached outbound conns keyed by "<network>|<peerPK hex>"
)

// chatHistoryLimits bounds the in-browser transcript. Unlike a public-facing
// persistent store this is our OWN sent/received messages, not an abuse
// surface, so rate-limiting/whitelisting are OFF (dropping would hide messages
// from the UI). MaxMessageSize sits at the frame ceiling so nothing valid is
// rejected; PerPeerCap bounds per-conversation memory; TotalCapBytes bounds
// overall; TTL is off (the tab is already ephemeral).
func chatHistoryLimits() history.Limits {
	return history.Limits{
		MaxMessageSize: message.MaxFrameSize,
		PerPeerCap:     500,
		TotalCapBytes:  8 * 1024 * 1024,
	}
}

// getChatStore lazily builds the MemStore on first use (skychat, file-xfer, and
// voice events can all append). Limits are static + valid, so NewMemStore can't
// realistically fail; on the impossible error path we log and return nil and
// callers no-op rather than panic.
func getChatStore() *history.MemStore {
	chatStoreOnce.Do(func() {
		s, err := history.NewMemStore(chatHistoryLimits())
		if err != nil {
			vlog("skychat: history init: " + err.Error())
			return
		}
		chatStore = s
	})
	return chatStore
}

// appendChat records a surfaced message in the shared history store. chatMsg.From
// is always the OTHER end (peer) for both directions (see the chatMsg doc), which
// maps directly onto history.Message.Peer; the actual sender (From) is the peer on
// an inbound message and empty on an outbound one.
func appendChat(m chatMsg) {
	st := getChatStore()
	if st == nil {
		return
	}
	if m.ID == "" {
		m.ID = newChatID()
	}
	if m.IsFile {
		chatMu.Lock()
		fileMeta[m.ID] = m
		chatMu.Unlock()
	}
	rec := history.Message{
		Peer:     m.From,
		Outgoing: m.Out,
		Text:     m.Text,
		ID:       m.ID,
		ReplyTo:  m.ReplyTo,
	}
	if m.TS > 0 {
		rec.Timestamp = time.UnixMilli(m.TS).UTC()
	}
	if !m.Out {
		rec.From = m.From // inbound: the sender is the peer
	}
	if err := st.Append(rec); err != nil {
		vlog("skychat: history append: " + err.Error())
	}
}

// storeToChatMsg projects a stored history.Message back to the chatMsg shape the
// page consumes, re-hydrating file-transfer fields from fileMeta by message ID.
func storeToChatMsg(rec history.Message) chatMsg {
	m := chatMsg{
		ID:      rec.ID,
		From:    rec.Peer,
		Text:    rec.Text,
		TS:      rec.Timestamp.UnixMilli(),
		Out:     rec.Outgoing,
		ReplyTo: rec.ReplyTo,
	}
	if rec.ID != "" {
		chatMu.Lock()
		if fm, ok := fileMeta[rec.ID]; ok {
			m.IsFile = true
			m.FileID = fm.FileID
			m.FileName = fm.FileName
			m.FileSize = fm.FileSize
			m.FileOK = fm.FileOK
		}
		chatMu.Unlock()
	}
	return m
}

// runBrowserSkychat is the in-process skychat AppFunc. It connects an app.Client
// (over the proc-manager net.Pipe) and listens on BOTH dmsg:1 (direct) and
// skynet:1 (over a route), serving each incoming connection's framed messages
// into the buffer. Mirrors native skychat (useDmsg+useSkynet) so a peer can reach
// it either way. Returns when ctx is canceled.
func runBrowserSkychat(ctx context.Context, _ []string) error {
	cl := app.NewClient(nil)
	defer cl.Close()
	chatClient = cl

	started := 0
	for _, n := range []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet} {
		lis, err := cl.Listen(n, skychatPort)
		if err != nil {
			// A missing networker (e.g. skynet before the router is up) shouldn't
			// kill the app — keep whatever listener(s) we got.
			vlog(fmt.Sprintf("skychat: listen %s:%d: %s", n, skychatPort, err.Error()))
			continue
		}
		started++
		vlog(fmt.Sprintf("skychat: listening on %s:%d", n, skychatPort))
		go func(l net.Listener) {
			<-ctx.Done()
			l.Close() //nolint:errcheck,gosec
		}(lis)
		go acceptChatLoop(lis)
	}
	if started == 0 {
		return fmt.Errorf("skychat: no listeners started")
	}
	// File transfer shares the same app.Client: listen on the file port over the
	// same networks so peers can send us files (see filexfer_js.go).
	startFileXferWasm(ctx, cl)
	// Voice: listens for inbound calls on the voice port over the same networks.
	startVoiceWasm(ctx, cl)
	<-ctx.Done()
	return nil
}

// acceptChatLoop accepts connections on one listener until it closes.
func acceptChatLoop(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		go readChatConn(message.NewConn(conn))
	}
}

// readChatConn reads framed messages off one peer connection into the buffer
// until the peer closes or errors. Used for both accepted (inbound) and dialed
// (outbound) conns — a skychat conn is bidirectional. When the peer sends an
// ack-requesting chat-msg (native `send --wait`), we reply with a chat-ack on the
// same conn so the sender's wait resolves instead of falsely timing out.
func readChatConn(conn *message.Conn) {
	defer conn.Close() //nolint:errcheck
	from := peerPKHex(conn)
	for {
		payload, err := conn.ReadFrame()
		if err != nil {
			return
		}
		text, ackID, replyTo, msgID := decodeChatPayload(payload)
		if ackID != "" {
			if b, mErr := (message.Envelope{Type: message.TypeAck, ID: ackID}).Marshal(); mErr == nil {
				if wErr := conn.WriteFrame(b); wErr != nil {
					vlog(fmt.Sprintf("skychat: ack to %s failed: %s", shortPK(from), wErr.Error()))
				}
			}
		}
		if text == "" {
			continue // an ack-only envelope, or empty — nothing to surface
		}
		appendChat(chatMsg{ID: msgID, From: from, Text: text, TS: nowMs(), Out: false, ReplyTo: replyTo})
		vlog(fmt.Sprintf("skychat: message from %s: %q", shortPK(from), text))
	}
}

// sendChat dials the peer on <network>:1 (reusing a cached conn) and writes one
// plain-text frame — the format native skychat accepts on its read loop. network
// is "dmsg" (default) or "skynet"; conns are cached per (network, peer) so the two
// transports don't clobber each other. The dial/connect/send steps are vlog'd so
// the chat window's log pane can surface them (like the skysocks-lite route setup).
func sendChat(pkHex, text, network, replyTo string) error {
	if chatClient == nil {
		return fmt.Errorf("skychat not started yet")
	}
	if text == "" {
		return fmt.Errorf("empty message")
	}
	var net appnet.Type
	switch network {
	case "", "dmsg":
		net = appnet.TypeDmsg
	case "skynet":
		net = appnet.TypeSkynet
	default:
		return fmt.Errorf("unknown network %q (use dmsg or skynet)", network)
	}
	var pk cipher.PubKey
	if err := pk.Set(pkHex); err != nil {
		return fmt.Errorf("bad peer pk: %w", err)
	}
	key := string(net) + "|" + pkHex
	chatMu.Lock()
	conn := chatConns[key]
	chatMu.Unlock()
	if conn == nil {
		vlog(fmt.Sprintf("skychat: dialing %s %s:%d…", net, shortPK(pkHex), skychatPort))
		c, err := chatClient.Dial(appnet.Addr{Net: net, PubKey: pk, Port: skychatPort})
		if err != nil {
			vlog(fmt.Sprintf("skychat: dial %s %s failed: %s", net, shortPK(pkHex), err.Error()))
			return fmt.Errorf("dial %s over %s: %w", shortPK(pkHex), net, err)
		}
		vlog(fmt.Sprintf("skychat: connected to %s over %s", shortPK(pkHex), net))
		conn = message.NewConn(c)
		chatMu.Lock()
		chatConns[key] = conn
		chatMu.Unlock()
		// Read replies on the same conn; drop it from the cache when it closes.
		go func() {
			readChatConn(conn)
			chatMu.Lock()
			delete(chatConns, key)
			chatMu.Unlock()
		}()
	}
	// Every DM rides a chat-msg envelope carrying a stable id, so it is
	// addressable — replies (and, in follow-ups, receipts/deletes) reference it.
	// The native read loop already decodes chat-msg envelopes (message.ParseEnvelope),
	// so this stays wire-compatible; ReplyTo is omitempty, set only for a reply.
	id := newChatID()
	wire, mErr := (message.Envelope{Type: message.TypeMsg, ID: id, Body: text, ReplyTo: replyTo}).Marshal()
	if mErr != nil {
		return fmt.Errorf("encode: %w", mErr)
	}
	if err := conn.WriteFrame(wire); err != nil {
		chatMu.Lock()
		delete(chatConns, key)
		chatMu.Unlock()
		conn.Close() //nolint:errcheck,gosec
		return fmt.Errorf("send: %w", err)
	}
	vlog(fmt.Sprintf("skychat: sent %d bytes to %s over %s", len(text), shortPK(pkHex), net))
	appendChat(chatMsg{ID: id, From: pkHex, Text: text, TS: nowMs(), Out: true, ReplyTo: replyTo})
	return nil
}

// newChatID mints a short random hex id for an outbound chat-msg envelope.
func newChatID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", nowMs())
	}
	return fmt.Sprintf("%x", b[:])
}

// decodeChatPayload extracts the message text from a frame, plus the ack id to
// reply with (non-empty only when the peer's chat-msg requested an ack — native
// `send --wait`). Native skychat sends either plain UTF-8 bytes (default) or a
// chat-* JSON envelope: we return the body for a "chat-msg", ("","") for a
// "chat-ack", else the raw bytes. Recognition (message.ParseEnvelope) is
// conservative — a known chat-* type only — so literal JSON typed into a chat
// still reaches the peer as plain text.
func decodeChatPayload(payload []byte) (text, ackID, replyTo, msgID string) {
	if env, ok := message.ParseEnvelope(payload); ok {
		switch env.Type {
		case message.TypeAck:
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

func peerPKHex(conn net.Conn) string {
	if a, ok := conn.RemoteAddr().(appnet.Addr); ok {
		return a.PubKey.Hex()
	}
	return conn.RemoteAddr().String()
}

func shortPK(pk string) string {
	if len(pk) > 8 {
		return pk[:8]
	}
	return pk
}

func nowMs() int64 { return time.Now().UnixMilli() }

// jsSkychatSend(peerPkHex, text[, network[, replyToID]]) → Promise<null>.
// network is "dmsg" (default) or "skynet".
func jsSkychatSend(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return js.Global().Get("Error").New("skychatSend(peerPkHex, text[, network])")
	}
	pkHex, text := args[0].String(), args[1].String()
	network := "dmsg"
	if len(args) >= 3 && args[2].Type() == js.TypeString {
		network = args[2].String()
	}
	replyTo := "" // optional 4th arg: the id of a message from skychatMessages() to quote
	if len(args) >= 4 && args[3].Type() == js.TypeString {
		replyTo = args[3].String()
	}
	return promise(func() (interface{}, error) {
		return nil, sendChat(pkHex, text, network, replyTo)
	})
}

// jsSkychatMessages() → JSON string of recent messages across all peers,
// newest last (up to 500). Backed by the shared history MemStore.
func jsSkychatMessages(js.Value, []js.Value) interface{} {
	out := []chatMsg{}
	if st := getChatStore(); st != nil {
		recs, _ := st.ListRecent(500) //nolint:errcheck
		for _, r := range recs {
			out = append(out, storeToChatMsg(r))
		}
	}
	b, _ := json.Marshal(out) //nolint:errcheck
	return string(b)
}

// jsSkychatHistory(peerPkHex[, limit]) → JSON string of that peer's messages,
// newest last (limit <= 0 → all). Parity with the native app's /history?peer=
// endpoint, now that both sides share pkg/skychat/history.
func jsSkychatHistory(_ js.Value, args []js.Value) interface{} {
	peer := ""
	if len(args) >= 1 && args[0].Type() == js.TypeString {
		peer = args[0].String()
	}
	limit := 0
	if len(args) >= 2 && args[1].Type() == js.TypeNumber {
		limit = args[1].Int()
	}
	out := []chatMsg{}
	if st := getChatStore(); st != nil && peer != "" {
		recs, _ := st.ListByPeer(peer, limit) //nolint:errcheck
		for _, r := range recs {
			out = append(out, storeToChatMsg(r))
		}
	}
	b, _ := json.Marshal(out) //nolint:errcheck
	return string(b)
}
