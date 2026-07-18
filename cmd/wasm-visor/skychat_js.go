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
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// skychatPort is skychat's well-known routing port: native skychat dials peers
// at appnet.Addr{...Port: 1}, so the in-process app must listen there to be
// reachable, and dials peers there in turn.
const skychatPort = 1

// chatMsg is one buffered message surfaced to the page (skychatMessages()).
type chatMsg struct {
	From string `json:"from"` // peer PK hex (the other end)
	Text string `json:"text"`
	TS   int64  `json:"ts"`  // unix milliseconds
	Out  bool   `json:"out"` // true = we sent it, false = received

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
	chatMu     sync.Mutex
	chatLog    []chatMsg
	chatClient *app.Client
	chatConns  = map[string]*message.Conn{} // cached outbound conns keyed by "<network>|<peerPK hex>"
)

func appendChat(m chatMsg) {
	chatMu.Lock()
	chatLog = append(chatLog, m)
	if len(chatLog) > 500 { // keep the last 500; a browser tab isn't a history store
		chatLog = chatLog[len(chatLog)-500:]
	}
	chatMu.Unlock()
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
		text, ackID := decodeChatPayload(payload)
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
		appendChat(chatMsg{From: from, Text: text, TS: nowMs(), Out: false})
		vlog(fmt.Sprintf("skychat: message from %s: %q", shortPK(from), text))
	}
}

// sendChat dials the peer on <network>:1 (reusing a cached conn) and writes one
// plain-text frame — the format native skychat accepts on its read loop. network
// is "dmsg" (default) or "skynet"; conns are cached per (network, peer) so the two
// transports don't clobber each other. The dial/connect/send steps are vlog'd so
// the chat window's log pane can surface them (like the skysocks-lite route setup).
func sendChat(pkHex, text, network string) error {
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
	if err := conn.WriteFrame([]byte(text)); err != nil {
		chatMu.Lock()
		delete(chatConns, key)
		chatMu.Unlock()
		conn.Close() //nolint:errcheck,gosec
		return fmt.Errorf("send: %w", err)
	}
	vlog(fmt.Sprintf("skychat: sent %d bytes to %s over %s", len(text), shortPK(pkHex), net))
	appendChat(chatMsg{From: pkHex, Text: text, TS: nowMs(), Out: true})
	return nil
}

// decodeChatPayload extracts the message text from a frame, plus the ack id to
// reply with (non-empty only when the peer's chat-msg requested an ack — native
// `send --wait`). Native skychat sends either plain UTF-8 bytes (default) or a
// chat-* JSON envelope: we return the body for a "chat-msg", ("","") for a
// "chat-ack", else the raw bytes. Recognition (message.ParseEnvelope) is
// conservative — a known chat-* type only — so literal JSON typed into a chat
// still reaches the peer as plain text.
func decodeChatPayload(payload []byte) (text, ackID string) {
	if env, ok := message.ParseEnvelope(payload); ok {
		switch env.Type {
		case message.TypeAck:
			return "", ""
		case message.TypeMsg:
			if env.Ack && env.ID != "" {
				return env.Body, env.ID
			}
			return env.Body, ""
		}
	}
	return string(payload), ""
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

// jsSkychatSend(peerPkHex, text[, network]) → Promise<null> (rejects on error).
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
	return promise(func() (interface{}, error) {
		return nil, sendChat(pkHex, text, network)
	})
}

// jsSkychatMessages() → JSON string of buffered messages (newest last).
func jsSkychatMessages(js.Value, []js.Value) interface{} {
	chatMu.Lock()
	b, _ := json.Marshal(chatLog) //nolint:errcheck
	chatMu.Unlock()
	return string(b)
}
