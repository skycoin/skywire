// Package dm controller_receipts_test.go — the delivery-status half of the
// controller: chat-ack / chat-read receipts reported to the app (OnReceipt),
// and the non-blocking RequestAck send that backs a UI's "sent → received →
// read" ticks, including its stale-conn recovery.
//
// These behaviours used to live in the native app (handleConn + dropStaleConn)
// and moved here with the DM core; their coverage moved with them.
package dm

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

type receipt struct{ peer, id, kind string }

// A chat-read frame is consumed like an ack — reported to the app, never
// surfaced as a chat line. Without the explicit case it would fall through to
// the plain-text path and show the raw JSON as a message.
func TestController_ChatReadReportedNotSurfaced(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)

	got := make(chan receipt, 4)
	surfaced := make(chan Event, 4)
	ctrlB := New(Config{
		Client:    &memClient{hub, pkB},
		Networks:  []appnet.Type{appnet.TypeDmsg},
		OnEvent:   func(ev Event) { surfaced <- ev },
		OnReceipt: func(peer, id, kind string) { got <- receipt{peer, id, kind} },
	})
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlB.Start(context.Background())                      //nolint:errcheck
	_ = ctrlA.Start(context.Background())                      //nolint:errcheck
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint:errcheck

	// A reads B's message, so A sends B a chat-read for it.
	env, err := (message.Envelope{Type: message.TypeRead, ID: "m42"}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrlA.SendRaw(context.Background(), pkB, env); err != nil {
		t.Fatalf("send read receipt: %v", err)
	}

	select {
	case r := <-got:
		if r.kind != message.TypeRead || r.id != "m42" || r.peer != pkA.Hex() {
			t.Errorf("receipt = %+v, want chat-read m42 from %s", r, pkA.Hex())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no receipt reported for an inbound chat-read")
	}
	select {
	case ev := <-surfaced:
		t.Errorf("a chat-read must not surface as a message, got %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

// An inbound chat-ack is reported too — that's what advances a bubble from
// "sent" to "received" — while still satisfying a WaitAck waiter.
func TestController_ChatAckReported(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)

	got := make(chan receipt, 4)
	ctrlA := New(Config{
		Client:    &memClient{hub, pkA},
		Networks:  []appnet.Type{appnet.TypeDmsg},
		OnEvent:   func(Event) {},
		OnReceipt: func(peer, id, kind string) { got <- receipt{peer, id, kind} },
	})
	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlB.Start(context.Background())                      //nolint:errcheck
	_ = ctrlA.Start(context.Background())                      //nolint:errcheck
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint:errcheck

	res, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "ack me", SendOpts{WaitAck: 2 * time.Second})
	if err != nil || !res.Acked {
		t.Fatalf("send: err=%v acked=%v", err, res.Acked)
	}
	select {
	case r := <-got:
		if r.kind != message.TypeAck || r.id != res.ID {
			t.Errorf("receipt = %+v, want chat-ack for %s", r, res.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an inbound chat-ack was not reported")
	}
}

// RequestAck asks the peer for an ack but must not block the caller — that's
// the difference from WaitAck, and what lets a UI return the id immediately.
func TestController_RequestAckDoesNotBlock(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)

	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	ctrlA := New(Config{
		Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg},
		OnEvent: func(Event) {}, StaleAckWindow: 2 * time.Second,
	})
	_ = ctrlB.Start(context.Background())                      //nolint:errcheck
	_ = ctrlA.Start(context.Background())                      //nolint:errcheck
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint:errcheck

	start := time.Now()
	res, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "status me", SendOpts{RequestAck: true})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("RequestAck send took %v — it must not wait for the ack", took)
	}
	if res.ID == "" {
		t.Error("RequestAck must mint an id, or the UI has nothing to track the bubble by")
	}
	if res.Acked {
		t.Error("RequestAck reports no verdict of its own; the ack arrives via OnReceipt")
	}
	// The peer still acks (ack=true went out on the wire), and the conn stays.
	time.Sleep(300 * time.Millisecond)
	if !ctrlA.HasConn(pkB) {
		t.Error("an acked conn must stay cached")
	}
}

// A half-open conn accepts the write but the frame never lands, so no ack comes
// back. After StaleAckWindow the conn is evicted, so the next send redials
// instead of writing into the void again.
func TestController_RequestAckEvictsSilentConn(t *testing.T) {
	pk := mustPK(t)
	c := New(Config{StaleAckWindow: 20 * time.Millisecond, Log: func(string, ...interface{}) {}})

	// Seed the cache with a conn nobody is reading from.
	raw, peerEnd := net.Pipe()
	t.Cleanup(func() { _ = raw.Close(); _ = peerEnd.Close() }) //nolint:errcheck
	conn := message.NewConn(memConn{Conn: raw, raddr: appnet.Addr{Net: appnet.TypeDmsg, PubKey: pk}})
	c.mu.Lock()
	c.conns[pk] = conn
	c.mu.Unlock()

	never := make(chan struct{})
	c.watchAck(pk, conn, never, nil, c.cfg.StaleAckWindow)

	if c.HasConn(pk) {
		t.Error("a conn whose ack never came back should be evicted after the window")
	}
}

// The eviction is pointer-guarded: if a fresh dial already replaced the cached
// conn, the late watcher must leave the new one alone.
func TestController_RequestAckLeavesReplacedConn(t *testing.T) {
	pk := mustPK(t)
	c := New(Config{StaleAckWindow: 20 * time.Millisecond, Log: func(string, ...interface{}) {}})

	oldRaw, oldPeer := net.Pipe()
	newRaw, newPeer := net.Pipe()
	t.Cleanup(func() { //nolint:errcheck
		_ = oldRaw.Close()
		_ = oldPeer.Close()
		_ = newRaw.Close()
		_ = newPeer.Close()
	})
	addr := appnet.Addr{Net: appnet.TypeDmsg, PubKey: pk}
	oldConn := message.NewConn(memConn{Conn: oldRaw, raddr: addr})
	newConn := message.NewConn(memConn{Conn: newRaw, raddr: addr})
	c.mu.Lock()
	c.conns[pk] = newConn // a fresh dial got there first
	c.mu.Unlock()

	never := make(chan struct{})
	c.watchAck(pk, oldConn, never, nil, c.cfg.StaleAckWindow) // watching the OLD conn

	c.mu.Lock()
	got := c.conns[pk]
	c.mu.Unlock()
	if got != newConn {
		t.Error("must not evict a conn that a fresh dial already replaced")
	}
}

// An ack that arrives in time keeps the conn and releases the waiter.
func TestController_WatchAckKeepsAckedConn(t *testing.T) {
	pk := mustPK(t)
	c := New(Config{StaleAckWindow: 5 * time.Second, Log: func(string, ...interface{}) {}})

	raw, peerEnd := net.Pipe()
	t.Cleanup(func() { _ = raw.Close(); _ = peerEnd.Close() }) //nolint:errcheck
	conn := message.NewConn(memConn{Conn: raw, raddr: appnet.Addr{Net: appnet.TypeDmsg, PubKey: pk}})
	c.mu.Lock()
	c.conns[pk] = conn
	c.mu.Unlock()

	acked := make(chan struct{}, 1)
	acked <- struct{}{}
	unregistered := false
	start := time.Now()
	c.watchAck(pk, conn, acked, func() { unregistered = true }, c.cfg.StaleAckWindow)

	if took := time.Since(start); took > time.Second {
		t.Errorf("watchAck returned after %v — an ack should release it at once", took)
	}
	if !c.HasConn(pk) {
		t.Error("an acked conn must stay cached")
	}
	if !unregistered {
		t.Error("the ack waiter must be released on the way out")
	}
}

// receiptKind classifies only receipts — a chat-msg or plain text is not one.
func TestReceiptKind(t *testing.T) {
	mk := func(e message.Envelope) []byte {
		b, err := e.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := []struct {
		name     string
		payload  []byte
		kind, id string
	}{
		{"ack", mk(message.Envelope{Type: message.TypeAck, ID: "a1"}), message.TypeAck, "a1"},
		{"read", mk(message.Envelope{Type: message.TypeRead, ID: "r1"}), message.TypeRead, "r1"},
		{"chat-msg", mk(message.Envelope{Type: message.TypeMsg, ID: "m1", Body: "hi"}), "", ""},
		{"idless ack", mk(message.Envelope{Type: message.TypeAck}), "", ""},
		{"plain text", []byte("just a message"), "", ""},
		{"malformed", []byte("{not json"), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, id := receiptKind(tc.payload)
			if kind != tc.kind || id != tc.id {
				t.Errorf("receiptKind = (%q, %q), want (%q, %q)", kind, id, tc.kind, tc.id)
			}
		})
	}
}
