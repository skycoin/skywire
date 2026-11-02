package dm

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// inbox stands in for the peers' dmsg-async inbox hosts: one host that holds
// for every key. A real host keeps the signer the visor verified as From.
type inbox struct {
	mu   sync.Mutex
	host cipher.PubKey
	next int
	msgs map[cipher.PubKey][]Parked
	refs map[string]bool
}

func newInbox(t *testing.T) *inbox {
	return &inbox{host: mustPK(t), msgs: map[cipher.PubKey][]Parked{}, refs: map[string]bool{}}
}

func (b *inbox) count(pk cipher.PubKey) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.msgs[pk])
}

// put deposits frame for to as from, as a host would, ignoring refs.
func (b *inbox) put(from, to cipher.PubKey, frame []byte, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	b.msgs[to] = append(b.msgs[to], Parked{ID: fmt.Sprint(b.next), Host: b.host, From: from, Frame: frame, At: at})
}

type inboxParker struct {
	b    *inbox
	self cipher.PubKey
}

func (p inboxParker) Park(_ context.Context, pk cipher.PubKey, frame []byte, ref string) (cipher.PubKey, error) {
	p.b.mu.Lock()
	seen := p.b.refs[p.self.Hex()+ref]
	p.b.refs[p.self.Hex()+ref] = true
	p.b.mu.Unlock()
	if !seen {
		p.b.put(p.self, pk, frame, time.Now())
	}
	return p.b.host, nil
}

func (p inboxParker) Unpark(context.Context) ([]Parked, error) {
	p.b.mu.Lock()
	defer p.b.mu.Unlock()
	return append([]Parked(nil), p.b.msgs[p.self]...), nil
}

func (p inboxParker) Ack(_ context.Context, done []Parked) error {
	p.b.mu.Lock()
	defer p.b.mu.Unlock()
	var keep []Parked
	for _, m := range p.b.msgs[p.self] {
		acked := false
		for _, d := range done {
			acked = acked || d.ID == m.ID
		}
		if !acked {
			keep = append(keep, m)
		}
	}
	p.b.msgs[p.self] = keep
	return nil
}

func boltStore(t *testing.T) *history.BoltStore {
	t.Helper()
	s, err := history.NewBoltStore(filepath.Join(t.TempDir(), "h.db"), history.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) //nolint:errcheck
	return s
}

// A sends to B while B is offline: the message is parked. B comes back, files
// it at the time the host took it and acks it to A. A replayed copy is dropped.
func TestController_ParkedMessageReachesThePeer(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	box := newInbox(t)
	recA, recB := &recorder{}, &recorder{}
	acked := make(chan string, 1)
	ctrlA := New(Config{
		Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: recA.on,
		Parker:    inboxParker{box, pkA},
		OnReceipt: func(_, id, kind string) { acked <- kind + ":" + id },
	})
	if err := ctrlA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctrlA.Close() }) //nolint:errcheck

	res, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "while you were out", SendOpts{RequestAck: true})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !res.Parked || res.ParkHost != box.host || res.Network != NetworkParked {
		t.Fatalf("result = %+v, want parked on %s", res, box.host)
	}
	if box.count(pkB) != 1 {
		t.Fatalf("inbox holds %d for B, want 1", box.count(pkB))
	}
	waitFor(t, recA, func(e Event) bool { return e.Dir == "out" && e.ID == res.ID && e.Network == string(NetworkParked) })
	box.mu.Lock()
	at := box.msgs[pkB][0].At.UTC()
	box.mu.Unlock()

	storeB := boltStore(t)
	ctrlB := New(Config{
		Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: recB.on,
		Store: storeB, Parker: inboxParker{box, pkB},
	})
	if err := ctrlB.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctrlB.Close() }) //nolint:errcheck
	if n := ctrlB.Unpark(context.Background()); n != 1 {
		t.Fatalf("Unpark filed %d, want 1", n)
	}
	in := waitFor(t, recB, func(e Event) bool { return e.Dir == "in" && e.ID == res.ID })
	if in.Peer != pkA.Hex() || in.Text != "while you were out" || in.Network != string(NetworkParked) || !in.TS.Equal(at) {
		t.Errorf("inbound = %+v", in)
	}
	if box.count(pkB) != 0 {
		t.Error("filed message left on the host")
	}
	select {
	case got := <-acked:
		if got != message.TypeAck+":"+res.ID {
			t.Errorf("receipt = %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("A got no ack for the parked message")
	}

	// A host that replays the message gets it removed, not filed twice.
	frame, err := (message.Envelope{Type: message.TypeMsg, ID: res.ID, Body: "while you were out"}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	box.put(pkA, pkB, frame, time.Now())
	if n := ctrlB.Unpark(context.Background()); n != 0 {
		t.Errorf("replay filed %d", n)
	}
	if box.count(pkB) != 0 {
		t.Error("replay left on the host")
	}
	msgs, err := storeB.ListByPeer(pkA.Hex(), 0)
	if err != nil || len(msgs) != 1 {
		t.Errorf("history = %d messages (%v), want 1", len(msgs), err)
	}
}

// A blocked peer's parked message is removed without surfacing.
func TestController_ParkedFromABlockedPeerIsDropped(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	box := newInbox(t)
	rec := &recorder{}
	ctrlB := New(Config{
		Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: rec.on,
		Parker:         inboxParker{box, pkB},
		PreHandleFrame: func(peer cipher.PubKey, _ []byte) bool { return peer == pkA },
	})
	frame, err := (message.Envelope{Type: message.TypeMsg, ID: "x1", Body: "spam"}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	box.put(pkA, pkB, frame, time.Now())
	if n := ctrlB.Unpark(context.Background()); n != 0 {
		t.Errorf("filed %d", n)
	}
	if box.count(pkB) != 0 {
		t.Error("blocked message left on the host")
	}
	if _, ok := rec.find(func(Event) bool { return true }); ok {
		t.Error("blocked message surfaced")
	}
}

// Without a Parker, or for a message with no id, a send no network delivers
// still fails.
func TestController_UnparkableSendFails(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	box := newInbox(t)
	for name, cfg := range map[string]Config{
		"no parker": {},
		"no id":     {Parker: inboxParker{box, pkA}, RawText: true},
	} {
		cfg.Client, cfg.Networks, cfg.OnEvent = &memClient{hub, pkA}, []appnet.Type{appnet.TypeDmsg}, func(Event) {}
		ctrl := New(cfg)
		res, err := ctrl.Send(context.Background(), pkB, appnet.TypeDmsg, "nobody home", SendOpts{})
		if err == nil || res.Parked {
			t.Errorf("%s: err=%v parked=%v, want an error", name, err, res.Parked)
		}
		_ = ctrl.Close() //nolint:errcheck
	}
	if box.count(pkB) != 0 {
		t.Error("parked anyway")
	}
}

// A peer that takes the write but never acks is how a dead conn shows. A send
// that waits for the ack, or watches for it, parks the message then.
func TestController_UnackedSendIsParked(t *testing.T) {
	hub := newMemHub()
	pkA, pkB := mustPK(t), mustPK(t)
	box := newInbox(t)
	ctrlB := New(Config{
		Client: &memClient{hub, pkB}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {},
		PreHandleFrame: func(cipher.PubKey, []byte) bool { return true },
	})
	ctrlA := New(Config{
		Client: &memClient{hub, pkA}, Networks: []appnet.Type{appnet.TypeDmsg}, OnEvent: func(Event) {},
		Parker: inboxParker{box, pkA}, StaleAckWindow: 50 * time.Millisecond,
	})
	for _, c := range []*Controller{ctrlB, ctrlA} {
		if err := c.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = ctrlA.Close(); _ = ctrlB.Close() }) //nolint:errcheck

	res, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "waited", SendOpts{WaitAck: 50 * time.Millisecond})
	if err != nil || res.Acked || !res.Parked || res.ParkHost != box.host {
		t.Fatalf("wait: err=%v result=%+v, want parked", err, res)
	}
	if _, err := ctrlA.Send(context.Background(), pkB, appnet.TypeDmsg, "watched", SendOpts{RequestAck: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for box.count(pkB) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if box.count(pkB) != 2 {
		t.Errorf("inbox holds %d for B, want 2", box.count(pkB))
	}
}
