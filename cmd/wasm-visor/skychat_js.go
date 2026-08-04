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
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
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

	// Deleted marks a message deleted-for-everyone: the page renders a "this
	// message was deleted" placeholder and the text is blanked in the projection.
	Deleted bool `json:"deleted,omitempty"`

	// Status is the delivery-status tick on our OWN (Out=true) messages, advanced
	// by peer receipts: "received" (peer's app acked) → "read" (peer's UI showed
	// it). Empty on inbound messages and before any receipt arrives (rendered as
	// a plain "sent" tick). Not persisted — a live-only lifecycle like Deleted.
	Status string `json:"status,omitempty"`

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
	chatCtrl   *dm.Controller // shared 1:1 DM core (pkg/skychat/dm): conns, read loop, envelopes, send
	// deletedChat holds the ids of messages deleted-for-everyone (by us or the
	// peer). storeToChatMsg tombstones them on projection. Not persisted — a
	// live-only tombstone, symmetric with the native app's dm-status "deleted".
	deletedChat = map[string]struct{}{}
	// receiptChat maps our own sent-message id → delivery status ("received" or
	// "read"), set from the controller's OnReceipt. storeToChatMsg stamps it onto
	// the outgoing projection so the page renders the tick. Live-only like
	// deletedChat; symmetric with the native app's dm-status "received"/"read".
	receiptChat = map[string]string{}
)

// markChatDeleted tombstones the message with the given id (guarded by chatMu).
func markChatDeleted(id string) {
	if id == "" {
		return
	}
	chatMu.Lock()
	deletedChat[id] = struct{}{}
	chatMu.Unlock()
}

// markChatReceipt records a delivery receipt for one of our sent messages. A late
// "received" never downgrades a message already marked "read" (guarded by chatMu).
func markChatReceipt(id, kind string) {
	if id == "" || (kind != "received" && kind != "read") {
		return
	}
	chatMu.Lock()
	if !(kind == "received" && receiptChat[id] == "read") {
		receiptChat[id] = kind
	}
	chatMu.Unlock()
}

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
		if _, del := deletedChat[rec.ID]; del {
			m.Deleted = true
			m.Text = ""
			m.IsFile = false
		} else if fm, ok := fileMeta[rec.ID]; ok {
			m.IsFile = true
			m.FileID = fm.FileID
			m.FileName = fm.FileName
			m.FileSize = fm.FileSize
			m.FileOK = fm.FileOK
		}
		// Delivery tick on our own sent messages (receipts only apply outbound).
		if m.Out {
			if s, ok := receiptChat[rec.ID]; ok {
				m.Status = s
			}
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

	// The 1:1 DM path (listen/accept, read loop, envelopes, send) is the shared
	// pkg/skychat/dm.Controller — the same core the native app uses. It listens
	// on dmsg:1 + skynet:1, surfaces every message (in + the outbound mirror)
	// through OnEvent → appendChat (which persists to the in-memory history
	// store the page polls). AlwaysID keeps every DM addressable (id-keyed
	// buffer, quoted replies). Store is nil here — appendChat owns persistence
	// so file entries (fileMeta) and DM text share one MemStore writer.
	chatCtrl = dm.New(dm.Config{
		Client:   cl,
		Networks: []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet},
		Port:     skychatPort,
		AlwaysID: true,
		OnEvent: func(ev dm.Event) {
			appendChat(chatMsg{
				ID: ev.ID, From: ev.Peer, Text: ev.Text,
				TS: ev.TS.UnixMilli(), Out: ev.Dir == "out", ReplyTo: ev.ReplyTo,
			})
			vlog(fmt.Sprintf("skychat: %s %s: %q", ev.Dir, shortPK(ev.Peer), ev.Text))
		},
		// A peer deleted (for everyone) a message they sent us — tombstone it.
		OnDelete: func(peer, id string) {
			markChatDeleted(id)
			vlog(fmt.Sprintf("skychat: %s deleted message %s", shortPK(peer), id))
		},
		// A receipt about a message WE sent — advance its delivery tick. kind is
		// the envelope type: chat-ack → received, chat-read → read.
		OnReceipt: func(peer, id, kind string) {
			switch kind {
			case message.TypeAck:
				markChatReceipt(id, "received")
			case message.TypeRead:
				markChatReceipt(id, "read")
			}
			vlog(fmt.Sprintf("skychat: %s %s message %s", shortPK(peer), kind, id))
		},
		Log: func(format string, args ...interface{}) { vlog(fmt.Sprintf(format, args...)) },
	})
	if err := chatCtrl.Start(ctx); err != nil {
		return err
	}
	go func() { <-ctx.Done(); _ = chatCtrl.Close() }()

	// File transfer shares the same app.Client: listen on the file port over the
	// same networks so peers can send us files (see filexfer_js.go).
	startFileXferWasm(ctx, cl)
	// Voice: listens for inbound calls on the voice port over the same networks.
	startVoiceWasm(ctx, cl)
	<-ctx.Done()
	return nil
}

// sendChat delivers text to the peer via the shared DM controller. network is
// "dmsg" (default) or "skynet". The controller mints the message id (AlwaysID),
// frames the chat-msg envelope (carrying replyTo when set), dials/reuses the
// conn, and surfaces the outbound mirror through OnEvent → appendChat — so this
// no longer buffers or frames anything itself.
func sendChat(pkHex, text, network, replyTo string) error {
	if chatCtrl == nil {
		return fmt.Errorf("skychat not started yet")
	}
	if text == "" {
		return fmt.Errorf("empty message")
	}
	var net appnet.Type
	auto := false
	switch network {
	case "", "skynet":
		net = appnet.TypeSkynet
	case "dmsg":
		net = appnet.TypeDmsg
	case "auto":
		auto = true // controller picks: reuse warm conn, else dmsg-now + warm skynet
	default:
		return fmt.Errorf("unknown network %q (use auto, dmsg or skynet)", network)
	}
	var pk cipher.PubKey
	if err := pk.Set(pkHex); err != nil {
		return fmt.Errorf("bad peer pk: %w", err)
	}
	// RequestAck rides an id'd envelope with ack=true (non-blocking): the peer
	// auto-acks, surfacing as a "received" receipt via OnReceipt so the bubble's
	// tick advances. The peer's "read" receipt follows when its UI displays it.
	_, err := chatCtrl.Send(context.Background(), pk, net, text, dm.SendOpts{ReplyTo: replyTo, RequestAck: true, Auto: auto})
	return err
}

// newChatID mints a short random hex id for a file-entry chatMsg (DM text ids
// come from the controller; file transfers surface through appendChat directly).
func newChatID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", nowMs())
	}
	return fmt.Sprintf("%x", b[:])
}

func shortPK(pk string) string {
	if len(pk) > 8 {
		return pk[:8]
	}
	return pk
}

func nowMs() int64 { return time.Now().UnixMilli() }

// jsSkychatSend(peerPkHex, text[, network[, replyToID]]) → Promise<null>.
// network is "auto" (default), "skynet", or "dmsg".
func jsSkychatSend(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return js.Global().Get("Error").New("skychatSend(peerPkHex, text[, network])")
	}
	pkHex, text := args[0].String(), args[1].String()
	network := "auto"
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

// jsSkychatDelete(peerPkHex, id) → Promise<null>. Deletes the message for
// everyone: sends a chat-delete to the peer (their app tombstones it via
// OnDelete) and tombstones our own copy immediately (best-effort — if the send
// fails because the peer is offline, our copy is still marked deleted).
func jsSkychatDelete(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return js.Global().Get("Error").New("skychatDelete(peerPkHex, id)")
	}
	pkHex, id := args[0].String(), args[1].String()
	return promise(func() (interface{}, error) {
		if chatCtrl == nil {
			return nil, fmt.Errorf("skychat not started yet")
		}
		var pk cipher.PubKey
		if err := pk.Set(pkHex); err != nil {
			return nil, fmt.Errorf("bad peer pk: %w", err)
		}
		err := chatCtrl.SendDelete(context.Background(), pk, id)
		markChatDeleted(id)
		return nil, err
	})
}

// jsSkychatReadReceipt(peerPkHex, id) → Promise<null>. Tells the peer we've read
// their message id so their bubble's tick advances to read. Best-effort — a peer
// that's offline simply never sees it (their tick stays at received).
func jsSkychatReadReceipt(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return js.Global().Get("Error").New("skychatReadReceipt(peerPkHex, id)")
	}
	pkHex, id := args[0].String(), args[1].String()
	return promise(func() (interface{}, error) {
		if chatCtrl == nil {
			return nil, fmt.Errorf("skychat not started yet")
		}
		var pk cipher.PubKey
		if err := pk.Set(pkHex); err != nil {
			return nil, fmt.Errorf("bad peer pk: %w", err)
		}
		return nil, chatCtrl.SendRead(context.Background(), pk, id)
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
