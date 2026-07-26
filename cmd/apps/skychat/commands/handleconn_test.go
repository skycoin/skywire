// Package commands cmd/apps/skychat/commands/handleconn_test.go
//
// Integration coverage for the inbound frame path: a net.Pipe wired as
// a skychat framed connection (RemoteAddr reporting a peer over the
// tcp-direct transport, via the tcpDirectConn shim) driving handleConn.
// Exercises the three receive outcomes — plain-text surfacing, a
// chat-msg envelope that is surfaced AND acked back over the same conn,
// and a chat-ack that is consumed internally without surfacing.
package commands

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// withHubAndPairing installs a fresh SSE hub, forces pairing off (so the
// pair-control fast path in handleConn is a no-op), ensures appLog is
// non-nil, and restores all three globals on cleanup.
func withHubAndPairing(t *testing.T) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	origHub, origPair := hub, pairEnable
	hub = newSSEHub()
	pairEnable = false
	t.Cleanup(func() {
		hub = origHub
		pairEnable = origPair
	})
}

// startHandleConn wires a net.Pipe as a skychat framed connection whose
// RemoteAddr reports peerPK over the tcp-direct transport, runs
// handleConn on the server end, and returns the client-side framed conn
// the test drives.
func startHandleConn(t *testing.T, peerPK cipher.PubKey) *framedConn {
	t.Helper()
	connsMu.Lock()
	if conns == nil {
		conns = make(map[cipher.PubKey]*framedConn)
	}
	connsMu.Unlock()

	serverRaw, clientRaw := net.Pipe()
	server := newFramedConn(&tcpDirectConn{Conn: serverRaw, rPK: peerPK})
	connsMu.Lock()
	conns[peerPK] = server
	connsMu.Unlock()
	go handleConn(server)

	t.Cleanup(func() {
		_ = clientRaw.Close() //nolint
		_ = serverRaw.Close() //nolint
		connsMu.Lock()
		delete(conns, peerPK)
		connsMu.Unlock()
	})
	return newFramedConn(clientRaw)
}

func waitForString(t *testing.T, ch <-chan string, d time.Duration) string { //nolint:unparam
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(d):
		t.Fatal("timed out waiting for an SSE message")
		return ""
	}
}

func TestHandleConn_PlainTextSurfaces(t *testing.T) {
	withHubAndPairing(t)
	peerPK, _ := cipher.GenerateKeyPair()
	sub, unsub := hub.subscribe()
	defer unsub()
	client := startHandleConn(t, peerPK)

	if err := client.WriteFrame([]byte("hello world")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := waitForString(t, sub, 2*time.Second)
	if !strings.Contains(got, `"message":"hello world"`) ||
		!strings.Contains(got, `"dir":"in"`) ||
		!strings.Contains(got, `"network":"tcp-direct"`) {
		t.Errorf("SSE event = %q, want inbound tcp-direct plain-text message", got)
	}
	if !strings.Contains(got, peerPK.Hex()) {
		t.Errorf("SSE event = %q, want sender = peer PK %s", got, peerPK.Hex())
	}
}

func TestHandleConn_ChatMsgEnvelopeAcksAndSurfaces(t *testing.T) {
	withHubAndPairing(t)
	peerPK, _ := cipher.GenerateKeyPair()
	sub, unsub := hub.subscribe()
	defer unsub()
	client := startHandleConn(t, peerPK)

	env := chatEnvelope{Type: chatTypeMsg, ID: "req-1", Body: "ack me", Ack: true}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteFrame(payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	// The server writes a chat-ack back over the same conn.
	ackFrame, err := client.ReadFrame()
	if err != nil {
		t.Fatalf("read ack frame: %v", err)
	}
	var ack chatEnvelope
	if err := json.Unmarshal(ackFrame, &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.Type != chatTypeAck || ack.ID != "req-1" {
		t.Errorf("ack envelope = %+v, want chat-ack id=req-1", ack)
	}

	// The body still surfaces as a normal inbound message.
	got := waitForString(t, sub, 2*time.Second)
	if !strings.Contains(got, `"message":"ack me"`) {
		t.Errorf("SSE event = %q, want unwrapped body 'ack me'", got)
	}
}

func TestHandleConn_ChatAckConsumedNotSurfaced(t *testing.T) {
	withHubAndPairing(t)
	peerPK, _ := cipher.GenerateKeyPair()
	sub, unsub := hub.subscribe()
	defer unsub()
	client := startHandleConn(t, peerPK)

	// A waiter registered for this id should fire when the inbound
	// chat-ack is consumed.
	waitCh, unreg := registerAckWaiter("ackid-9")
	defer unreg()
	env := chatEnvelope{Type: chatTypeAck, ID: "ackid-9"}
	payload, _ := json.Marshal(env) //nolint:errcheck
	if err := client.WriteFrame(payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("chat-ack was not delivered to the registered waiter")
	}
	// A chat-ack is not surfaced as a chat MESSAGE, but it does emit a
	// dm-status "received" control event so the sender's bubble advances.
	got := waitForString(t, sub, 2*time.Second)
	if strings.Contains(got, `"message"`) {
		t.Errorf("chat-ack must not surface as a chat message, got %q", got)
	}
	if !strings.Contains(got, `"channel":"dm-status"`) ||
		!strings.Contains(got, `"status":"received"`) ||
		!strings.Contains(got, `"id":"ackid-9"`) {
		t.Errorf("expected a dm-status received event for ackid-9, got %q", got)
	}
	if !strings.Contains(got, peerPK.Hex()) {
		t.Errorf("dm-status event should carry the peer PK %s, got %q", peerPK.Hex(), got)
	}
}

func TestHandleConn_ChatReadEmitsReadStatus(t *testing.T) {
	withHubAndPairing(t)
	peerPK, _ := cipher.GenerateKeyPair()
	sub, unsub := hub.subscribe()
	defer unsub()
	client := startHandleConn(t, peerPK)

	// The peer reports it has displayed a message WE sent (id "m-7").
	env := chatEnvelope{Type: chatTypeRead, ID: "m-7"}
	payload, _ := json.Marshal(env) //nolint:errcheck
	if err := client.WriteFrame(payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	// It must surface as a dm-status "read" event, not a chat message.
	got := waitForString(t, sub, 2*time.Second)
	if strings.Contains(got, `"message"`) {
		t.Errorf("chat-read must not surface as a chat message, got %q", got)
	}
	if !strings.Contains(got, `"channel":"dm-status"`) ||
		!strings.Contains(got, `"status":"read"`) ||
		!strings.Contains(got, `"id":"m-7"`) {
		t.Errorf("expected a dm-status read event for m-7, got %q", got)
	}
}
