//go:build js && wasm

// Package main — in-process skychat for the browser visor.
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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

// skychatPort is skychat's well-known routing port: native skychat dials peers
// at appnet.Addr{...Port: 1}, so the in-process app must listen there to be
// reachable, and dials peers there in turn.
const skychatPort = 1

// skychatMaxFrame bounds one framed message (matches native skychatMaxFrameSize).
const skychatMaxFrame = 64 * 1024

// chatMsg is one buffered message surfaced to the page (skychatMessages()).
type chatMsg struct {
	From string `json:"from"` // peer PK hex (the other end)
	Text string `json:"text"`
	TS   int64  `json:"ts"`  // unix milliseconds
	Out  bool   `json:"out"` // true = we sent it, false = received
}

var (
	chatMu     sync.Mutex
	chatLog    []chatMsg
	chatClient *app.Client
	chatConns  = map[cipher.PubKey]net.Conn{} // cached conns keyed by peer PK
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
// (over the proc-manager net.Pipe), listens on dmsg:1, and serves each incoming
// connection's framed messages into the buffer. Returns when ctx is canceled.
func runBrowserSkychat(ctx context.Context, _ []string) error {
	cl := app.NewClient(nil)
	defer cl.Close()
	chatClient = cl

	lis, err := cl.Listen(appnet.TypeDmsg, skychatPort)
	if err != nil {
		return fmt.Errorf("skychat: listen dmsg:%d: %w", skychatPort, err)
	}
	vlog(fmt.Sprintf("skychat: in-process app listening on dmsg:%d", skychatPort))
	go func() {
		<-ctx.Done()
		lis.Close() //nolint:errcheck,gosec
	}()
	for {
		conn, err := lis.Accept()
		if err != nil {
			return nil // listener closed (ctx done) — clean shutdown
		}
		go readChatConn(conn)
	}
}

// readChatConn reads framed messages off one peer connection into the buffer
// until the peer closes or errors. Used for both accepted (inbound) and dialed
// (outbound) conns — a skychat conn is bidirectional.
func readChatConn(conn net.Conn) {
	defer conn.Close() //nolint:errcheck
	from := peerPKHex(conn)
	for {
		payload, err := readFrame(conn)
		if err != nil {
			return
		}
		text := decodeChatPayload(payload)
		if text == "" {
			continue // an ack-only envelope, or empty — nothing to surface
		}
		appendChat(chatMsg{From: from, Text: text, TS: nowMs(), Out: false})
		vlog(fmt.Sprintf("skychat: message from %s: %q", shortPK(from), text))
	}
}

// sendChat dials the peer on dmsg:1 (reusing a cached conn) and writes one
// plain-text frame — the format native skychat accepts on its read loop.
func sendChat(pkHex, text string) error {
	if chatClient == nil {
		return fmt.Errorf("skychat not started yet")
	}
	if text == "" {
		return fmt.Errorf("empty message")
	}
	var pk cipher.PubKey
	if err := pk.Set(pkHex); err != nil {
		return fmt.Errorf("bad peer pk: %w", err)
	}
	chatMu.Lock()
	conn := chatConns[pk]
	chatMu.Unlock()
	if conn == nil {
		c, err := chatClient.Dial(appnet.Addr{Net: appnet.TypeDmsg, PubKey: pk, Port: skychatPort})
		if err != nil {
			return fmt.Errorf("dial %s: %w", shortPK(pkHex), err)
		}
		conn = c
		chatMu.Lock()
		chatConns[pk] = conn
		chatMu.Unlock()
		// Read replies on the same conn; drop it from the cache when it closes.
		go func() {
			readChatConn(conn)
			chatMu.Lock()
			delete(chatConns, pk)
			chatMu.Unlock()
		}()
	}
	if err := writeFrame(conn, []byte(text)); err != nil {
		chatMu.Lock()
		delete(chatConns, pk)
		chatMu.Unlock()
		conn.Close() //nolint:errcheck,gosec
		return fmt.Errorf("send: %w", err)
	}
	appendChat(chatMsg{From: pkHex, Text: text, TS: nowMs(), Out: true})
	return nil
}

// readFrame reads one length-prefixed frame (skychat wire format).
func readFrame(conn net.Conn) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > skychatMaxFrame {
		return nil, fmt.Errorf("skychat: bad frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeFrame(conn net.Conn, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

// decodeChatPayload extracts the message text from a frame. Native skychat sends
// either plain UTF-8 bytes (default) or a JSON envelope {type,id,body,ack}; we
// return the body for a "chat-msg" envelope, "" for an ack, else the raw bytes.
func decodeChatPayload(payload []byte) string {
	var env struct {
		Type string `json:"type"`
		Body string `json:"body"`
	}
	if json.Unmarshal(payload, &env) == nil && env.Type != "" {
		if env.Type == "chat-ack" {
			return ""
		}
		return env.Body
	}
	return string(payload)
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

// jsSkychatSend(peerPkHex, text) → Promise<null> (rejects on error).
func jsSkychatSend(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return js.Global().Get("Error").New("skychatSend(peerPkHex, text)")
	}
	pkHex, text := args[0].String(), args[1].String()
	return promise(func() (interface{}, error) {
		return nil, sendChat(pkHex, text)
	})
}

// jsSkychatMessages() → JSON string of buffered messages (newest last).
func jsSkychatMessages(js.Value, []js.Value) interface{} {
	chatMu.Lock()
	b, _ := json.Marshal(chatLog) //nolint:errcheck
	chatMu.Unlock()
	return string(b)
}
