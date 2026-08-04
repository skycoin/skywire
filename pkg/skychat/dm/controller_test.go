// Package dm controller_test.go — exercises the Controller end-to-end over an
// in-memory transport pair (no real dmsg/skynet): send/receive, quoted replies,
// and the peer-receipt ack round-trip.
package dm

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// --- in-memory transport ----------------------------------------------------

type memHub struct {
	mu        sync.Mutex
	listeners map[string]*memListener
}

func newMemHub() *memHub { return &memHub{listeners: map[string]*memListener{}} }

func key(n appnet.Type, pk cipher.PubKey, port routing.Port) string {
	return fmt.Sprintf("%s|%s|%d", n, pk.Hex(), uint16(port))
}

type memListener struct {
	accept chan net.Conn
	addr   appnet.Addr
	once   sync.Once
}

func (l *memListener) Accept() (net.Conn, error) {
	c, ok := <-l.accept
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *memListener) Close() error   { l.once.Do(func() { close(l.accept) }); return nil }
func (l *memListener) Addr() net.Addr { return l.addr }

// memConn overrides RemoteAddr so the controller sees an appnet.Addr carrying
// the peer's PK (which is how it derives the peer identity).
type memConn struct {
	net.Conn
	raddr appnet.Addr
}

func (c memConn) RemoteAddr() net.Addr { return c.raddr }

type memClient struct {
	hub    *memHub
	selfPK cipher.PubKey
}

func (m *memClient) Listen(n appnet.Type, port routing.Port) (net.Listener, error) {
	l := &memListener{accept: make(chan net.Conn, 8), addr: appnet.Addr{Net: n, PubKey: m.selfPK, Port: port}}
	m.hub.mu.Lock()
	m.hub.listeners[key(n, m.selfPK, port)] = l
	m.hub.mu.Unlock()
	return l, nil
}

func (m *memClient) Dial(addr appnet.Addr) (net.Conn, error) {
	m.hub.mu.Lock()
	l := m.hub.listeners[key(addr.Net, addr.PubKey, addr.Port)]
	m.hub.mu.Unlock()
	if l == nil {
		return nil, net.ErrClosed
	}
	dialerEnd, calleeEnd := net.Pipe()
	// The callee sees the dialer's PK; the dialer sees the callee's PK.
	l.accept <- memConn{Conn: calleeEnd, raddr: appnet.Addr{Net: addr.Net, PubKey: m.selfPK, Port: addr.Port}}
	return memConn{Conn: dialerEnd, raddr: addr}, nil
}

// --- helpers ----------------------------------------------------------------

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

type recorder struct {
	mu sync.Mutex
	ev []Event
}

func (r *recorder) on(e Event) { r.mu.Lock(); r.ev = append(r.ev, e); r.mu.Unlock() }
func (r *recorder) find(pred func(Event) bool) (Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.ev {
		if pred(e) {
			return e, true
		}
	}
	return Event{}, false
}

func waitFor(t *testing.T, r *recorder, pred func(Event) bool) Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e, ok := r.find(pred); ok {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event not observed within deadline; got %d events", len(r.ev))
	return Event{}
}

// --- tests ------------------------------------------------------------------

func TestController_SendReceive(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	recA, recB := &recorder{}, &recorder{}

	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: recB.on})
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: recA.on})
	if err := ctrlB.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ctrlA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint

	// Plain send A -> B.
	if _, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "hello B", SendOpts{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	inB := waitFor(t, recB, func(e Event) bool { return e.Dir == "in" && e.Text == "hello B" })
	if inB.Peer != pkA.Hex() {
		t.Errorf("inbound peer = %s, want %s", inB.Peer, pkA.Hex())
	}
	// A sees the outbound mirror.
	if _, ok := recA.find(func(e Event) bool { return e.Dir == "out" && e.Text == "hello B" && e.Peer == pkB.Hex() }); !ok {
		t.Error("A missing outbound mirror event")
	}
}

func TestController_QuotedReply(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	recB := &recorder{}
	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: recB.on})
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlB.Start(context.Background())                      //nolint
	_ = ctrlA.Start(context.Background())                      //nolint
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint

	res, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "a threaded reply", SendOpts{ReplyTo: "parent123"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == "" {
		t.Error("reply send should mint an id")
	}
	in := waitFor(t, recB, func(e Event) bool { return e.Text == "a threaded reply" })
	if in.ReplyTo != "parent123" {
		t.Errorf("reply_to propagated = %q, want parent123", in.ReplyTo)
	}
	if in.ID != res.ID {
		t.Errorf("inbound id %q != sent id %q", in.ID, res.ID)
	}
}

func TestController_AckRoundTrip(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlB.Start(context.Background())                      //nolint
	_ = ctrlA.Start(context.Background())                      //nolint
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint

	res, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "ack me", SendOpts{WaitAck: 2 * time.Second})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !res.Acked {
		t.Error("WaitAck send should be acked by a live peer")
	}
}

func TestController_ServeAndStats(t *testing.T) {
	// Serve injects an already-established conn (the TCP-direct shape): a
	// framed conn whose RemoteAddr reports an appnet.Addr. A frame written to
	// the other end should surface as an inbound event and bump InboundMsgs.
	pkPeer := mustPK(t)
	rec := &recorder{}
	ctrl := New(Config{Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: rec.on})
	t.Cleanup(func() { _ = ctrl.Close() }) //nolint

	near, far := net.Pipe()
	// far is what Serve reads; give it a RemoteAddr carrying the peer PK.
	go ctrl.Serve(memConn{Conn: far, raddr: appnet.Addr{Net: appnet.TypeDmsg, PubKey: pkPeer, Port: chatPort}})

	// Write a plain-text frame from the near end (peer -> us).
	go func() { _ = message.WriteFrame(near, []byte("injected hello")) }() //nolint
	in := waitFor(t, rec, func(e Event) bool { return e.Text == "injected hello" && e.Dir == "in" })
	if in.Peer != pkPeer.Hex() {
		t.Errorf("served-conn peer = %s, want %s", in.Peer, pkPeer.Hex())
	}
	if s := ctrl.Stats(); s.InboundMsgs != 1 {
		t.Errorf("InboundMsgs = %d, want 1", s.InboundMsgs)
	}
}

func TestController_DeleteForEveryone(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	var gotPeer, gotID string
	var mu sync.Mutex
	done := make(chan struct{}, 1)
	ctrlB := New(Config{
		Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg},
		OnEvent: func(Event) {},
		OnDelete: func(peer, id string) {
			mu.Lock()
			gotPeer, gotID = peer, id
			mu.Unlock()
			select {
			case done <- struct{}{}:
			default:
			}
		},
	})
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlB.Start(context.Background())                      //nolint
	_ = ctrlA.Start(context.Background())                      //nolint
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint

	const targetID = "msg-abc-123"
	if err := ctrlA.SendDelete(context.Background(), pkB, targetID); err != nil {
		t.Fatalf("SendDelete: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnDelete not called within deadline")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPeer != pkA.Hex() {
		t.Errorf("delete peer = %s, want %s", gotPeer, pkA.Hex())
	}
	if gotID != targetID {
		t.Errorf("delete id = %s, want %s", gotID, targetID)
	}
}

func TestController_ReadReceipt(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	var gotPeer, gotID, gotKind string
	var mu sync.Mutex
	done := make(chan struct{}, 1)
	// ctrlA sent a message; ctrlB reads it and sends a read receipt back, which
	// surfaces on ctrlA as OnReceipt(kind=chat-read) about A's own message.
	ctrlA := New(Config{
		Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg},
		OnEvent: func(Event) {},
		OnReceipt: func(peer, id, kind string) {
			mu.Lock()
			gotPeer, gotID, gotKind = peer, id, kind
			mu.Unlock()
			select {
			case done <- struct{}{}:
			default:
			}
		},
	})
	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlA.Start(context.Background())                      //nolint
	_ = ctrlB.Start(context.Background())                      //nolint
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint

	const targetID = "msg-read-42"
	if err := ctrlB.SendRead(context.Background(), pkA, targetID); err != nil {
		t.Fatalf("SendRead: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnReceipt not called within deadline")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPeer != pkB.Hex() {
		t.Errorf("receipt peer = %s, want %s", gotPeer, pkB.Hex())
	}
	if gotID != targetID {
		t.Errorf("receipt id = %s, want %s", gotID, targetID)
	}
	if gotKind != message.TypeRead {
		t.Errorf("receipt kind = %s, want %s", gotKind, message.TypeRead)
	}
}

func TestController_NoTransportDialErrors(t *testing.T) {
	// A controller with no Client (native --standalone): a send with no cached
	// conn must error cleanly, not panic.
	pk := mustPK(t)
	ctrl := New(Config{Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	t.Cleanup(func() { _ = ctrl.Close() }) //nolint
	if _, err := ctrl.Send(context.Background(), pk, appnet.TypeDmsg, "hi", SendOpts{}); err == nil {
		t.Error("send with no transport + no cached conn should error")
	}
}

func TestController_AckTimeoutNoPeer(t *testing.T) {
	// No peer listening: dial fails, Send returns an error (not a hang).
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {}})
	_ = ctrlA.Start(context.Background())   //nolint
	t.Cleanup(func() { _ = ctrlA.Close() }) //nolint

	_, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "nobody home", SendOpts{})
	if err == nil {
		t.Error("send to a non-listening peer should error")
	}
}

// TestController_AutoDmsgFirstThenSkynetUpgrade verifies auto mode: the first
// send (no warm conn) goes over dmsg immediately, a background warm upgrades the
// cached conn to skynet, and the next auto send reuses the skynet conn.
func TestController_AutoDmsgFirstThenSkynetUpgrade(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	recB := &recorder{}
	nets := []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet}

	ctrlB := New(Config{Client: &memClient{hub, pkB}, Networks: nets, OnEvent: recB.on})
	ctrlA := New(Config{Client: &memClient{hub, pkA}, Networks: nets, OnEvent: func(Event) {}})
	if err := ctrlB.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ctrlA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer ctrlA.Close()
	defer ctrlB.Close()

	// First auto send: no warm conn → dmsg (instant, no route setup).
	res, err := ctrlA.Send(context.Background(), pkB, "", "hi", SendOpts{Auto: true})
	if err != nil {
		t.Fatalf("first auto send: %v", err)
	}
	if res.Network != appnet.TypeDmsg {
		t.Fatalf("first auto send network = %q, want dmsg", res.Network)
	}
	waitFor(t, recB, func(e Event) bool { return e.Dir == "in" && e.Text == "hi" })

	// Background warm should upgrade the cached conn to skynet.
	cachedNet := func() appnet.Type {
		ctrlA.mu.Lock()
		defer ctrlA.mu.Unlock()
		if conn := ctrlA.conns[pkB]; conn != nil {
			if ra, ok := conn.RemoteAddr().(appnet.Addr); ok {
				return ra.Net
			}
		}
		return ""
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && cachedNet() != appnet.TypeSkynet {
		time.Sleep(10 * time.Millisecond)
	}
	if got := cachedNet(); got != appnet.TypeSkynet {
		t.Fatalf("cached conn network after warm = %q, want skynet", got)
	}

	// Second auto send reuses the warmed skynet conn.
	res2, err := ctrlA.Send(context.Background(), pkB, "", "yo", SendOpts{Auto: true})
	if err != nil {
		t.Fatalf("second auto send: %v", err)
	}
	if res2.Network != appnet.TypeSkynet {
		t.Fatalf("second auto send network = %q, want skynet", res2.Network)
	}
}
