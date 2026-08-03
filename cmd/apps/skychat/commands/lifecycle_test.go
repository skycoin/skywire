// Package commands cmd/apps/skychat/commands/lifecycle_test.go
//
// Coverage for the long-running / lifecycle surface that was still at 0%: the
// two SSE stream handlers (/sse and /events), the DM send handler, the
// controller's surfacing callback, the feature-gated mux registrations, the
// two visor-RPC pollers, and the RPC dial + watchdog lifecycle.
//
// These are all loops or wiring, which is why they were skipped: none of them
// return on their own. The pattern used throughout is to run the thing under a
// cancellable context in a goroutine, drive one observable cycle, cancel, and
// only then read what it produced — reading a recorder while its handler is
// still writing is a data race, not a test.
//
// Two properties here are worth more than the coverage:
//
//   - the /sse seed lines are tagged history:"true". The browser relies on that
//     tag to skip them; without it every reconnect re-renders the last 50
//     messages as if they were new (the "old messages show as new on reload"
//     bug).
//   - registerGroupHTTPHandlers / registerPresenceHTTPHandlers /
//     registerVoiceHTTPHandlers must register NOTHING when --pair-enable is
//     off, so a standalone skychat doesn't advertise endpoints that can only
//     ever 503.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/message"
	"github.com/skycoin/skywire/pkg/visor"
)

// --- harness ----------------------------------------------------------------

// withLifecycleEnv gives the test a private hub, a discard chatLog, pairing
// off, no CXO, and restores every global it touches.
func withLifecycleEnv(t *testing.T) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	withChatLog(t)

	orig := struct {
		hub          *sseHub
		pairEnable   bool
		osNotify     bool
		cxoEnable    bool
		cxoGroup     string
		chatCtrl     *dm.Controller
		seed         int
		pollInterval time.Duration
		rpcAddr      string
	}{hub, pairEnable, osNotify, cxoEnable, cxoGroup, chatCtrl, persistSeedCount, pairPollInterval, pairRPCAddr}

	hub = newSSEHub()
	pairEnable = false
	osNotify = false
	cxoEnable = false
	cxoGroup = ""
	persistSeedCount = 0

	t.Cleanup(func() {
		hub, pairEnable, osNotify = orig.hub, orig.pairEnable, orig.osNotify
		cxoEnable, cxoGroup, chatCtrl = orig.cxoEnable, orig.cxoGroup, orig.chatCtrl
		persistSeedCount, pairPollInterval, pairRPCAddr = orig.seed, orig.pollInterval, orig.rpcAddr
	})
}

// serveStream runs an SSE-style handler against a recorder under a cancellable
// request context, gives it `settle` to produce output, then cancels and waits
// for the handler to return before handing back the body. Reading the recorder
// any earlier races the handler's writes.
func serveStream(t *testing.T, h http.HandlerFunc, target string, settle time.Duration,
	during func()) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h(rr, req)
	}()

	if during != nil {
		during()
	}
	time.Sleep(settle)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream handler did not return after its request context was canceled")
	}
	return rr
}

// nonFlusher is an http.ResponseWriter without Flush, so the streaming
// handlers' capability check fires.
type nonFlusher struct {
	hdr  http.Header
	code int
	body strings.Builder
}

func (n *nonFlusher) Header() http.Header {
	if n.hdr == nil {
		n.hdr = http.Header{}
	}
	return n.hdr
}
func (n *nonFlusher) Write(b []byte) (int, error) { return n.body.Write(b) }
func (n *nonFlusher) WriteHeader(c int)           { n.code = c }

// --- sseHandler -------------------------------------------------------------

func TestSSEHandler_RejectsNonStreamingWriter(t *testing.T) {
	withLifecycleEnv(t)
	nf := &nonFlusher{}
	sseHandler(nf, httptest.NewRequest(http.MethodGet, "/sse", nil))
	if nf.code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when the writer cannot stream", nf.code)
	}
}

func TestSSEHandler_StreamsBroadcasts(t *testing.T) {
	withLifecycleEnv(t)

	rr := serveStream(t, sseHandler, "/sse", 300*time.Millisecond, func() {
		// Give the handler a moment to subscribe before broadcasting, or the
		// message is published to nobody.
		time.Sleep(150 * time.Millisecond)
		hub.broadcast(`{"sender":"peer","message":"live one"}`)
	})

	body := rr.Body.String()
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if rr.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("X-Accel-Buffering: no is required or a reverse proxy buffers the stream")
	}
	if !strings.Contains(body, ": connected") {
		t.Errorf("stream should open with a ': connected' keepalive; got %q", body)
	}
	if !strings.Contains(body, `data: {"sender":"peer","message":"live one"}`) {
		t.Errorf("broadcast not delivered; body = %q", body)
	}
}

// TestSSEHandler_SeedsHistoryTagged is the guard for the "old messages show as
// new on reload" bug: every seeded line must carry history:"true" so the
// browser skips it. An untagged seed renders as fresh traffic on every
// EventSource reconnect.
func TestSSEHandler_SeedsHistoryTagged(t *testing.T) {
	withLifecycleEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	persistSeedCount = 10

	peer, _ := cipher.GenerateKeyPair()
	if err := historyStore.Append(history.Message{
		Peer: peer.Hex(), From: peer.Hex(), Text: "seeded inbound", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	if err := historyStore.Append(history.Message{
		Peer: peer.Hex(), Outgoing: true, Text: "seeded outbound", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed append: %v", err)
	}

	rr := serveStream(t, sseHandler, "/sse", 200*time.Millisecond, nil)
	body := rr.Body.String()

	if !strings.Contains(body, "seeded inbound") || !strings.Contains(body, "seeded outbound") {
		t.Fatalf("history was not seeded into the stream; body = %q", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
			continue
		}
		if m["history"] != "true" {
			t.Errorf("seeded line %q lacks history:\"true\" — the browser would render it as new", line)
		}
	}
	// An outgoing seed is attributed to "self" so the UI can side it correctly.
	if !strings.Contains(body, `"sender":"self"`) {
		t.Error("an outgoing seeded message should be attributed to self")
	}
}

// --- eventsHandler ----------------------------------------------------------

func TestEventsHandler_RejectsNonStreamingWriter(t *testing.T) {
	withLifecycleEnv(t)
	nf := &nonFlusher{}
	eventsHandler(nf, httptest.NewRequest(http.MethodGet, "/events", nil))
	if nf.code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", nf.code)
	}
}

func TestEventsHandler_RejectsBadSince(t *testing.T) {
	withLifecycleEnv(t)
	rr := httptest.NewRecorder()
	eventsHandler(rr, httptest.NewRequest(http.MethodGet, "/events?since=not-a-number", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric since", rr.Code)
	}
}

func TestEventsHandler_StreamsStructuredEvents(t *testing.T) {
	withLifecycleEnv(t)

	peer, _ := cipher.GenerateKeyPair()
	rr := serveStream(t, eventsHandler, "/events", 300*time.Millisecond, func() {
		time.Sleep(150 * time.Millisecond)
		hub.publishEvent(chatEvent{
			ID: "ev-1", Channel: channelDM, Dir: "in", From: peer.Hex(), Text: "structured",
		})
	})

	body := rr.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Errorf("stream should open with ': connected'; got %q", body)
	}
	var found bool
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev chatEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Errorf("event line is not a chatEvent: %v (%q)", err, line)
			continue
		}
		if ev.ID == "ev-1" && ev.Text == "structured" && ev.Channel == channelDM {
			found = true
		}
	}
	if !found {
		t.Errorf("published event not delivered on /events; body = %q", body)
	}
}

// TestEventsHandler_ChannelFilterExcludes — ?channel=group must not deliver dm
// traffic, or `cli skychat events --channel group` is noise.
func TestEventsHandler_ChannelFilterExcludes(t *testing.T) {
	withLifecycleEnv(t)

	rr := serveStream(t, eventsHandler, "/events?channel=group", 300*time.Millisecond, func() {
		time.Sleep(150 * time.Millisecond)
		hub.publishEvent(chatEvent{ID: "dm-1", Channel: channelDM, Dir: "in", Text: "a dm"})
		hub.publishEvent(chatEvent{ID: "grp-1", Channel: channelGroup, Dir: "in", Text: "a group msg"})
	})

	body := rr.Body.String()
	if strings.Contains(body, "dm-1") {
		t.Error("channel=group delivered a dm event")
	}
	if !strings.Contains(body, "grp-1") {
		t.Errorf("channel=group did not deliver the group event; body = %q", body)
	}
}

// --- messageHandler ---------------------------------------------------------

func TestMessageHandler_Validation(t *testing.T) {
	withLifecycleEnv(t)
	h := messageHandler(context.Background())
	pk, _ := cipher.GenerateKeyPair()

	cases := []struct{ name, body string }{
		{"malformed json", `{"recipient":`},
		{"bad recipient", `{"recipient":"not-a-pk","message":"x"}`},
		{"unknown network", `{"recipient":"` + pk.Hex() + `","message":"x","network":"carrier-pigeon"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h(rr, httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(c.body)))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestMessageHandler_SendsPlainAndReceipts(t *testing.T) {
	withLifecycleEnv(t)
	cc := newCapturingClient()
	chatCtrl = dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
	})

	h := messageHandler(context.Background())
	pk, _ := cipher.GenerateKeyPair()

	// Plain send: byte-identical wire (no envelope), empty 200 body.
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/message",
		strings.NewReader(`{"recipient":"`+pk.Hex()+`","message":"plain hello"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("plain send status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	select {
	case raw := <-cc.frames:
		if string(raw) != "plain hello" {
			t.Errorf("wire payload = %q, want the bare text (a default send must stay envelope-free)", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the wire on a plain send")
	}

	// Receipts send: id'd envelope, non-blocking, returns the wire id.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/message",
		strings.NewReader(`{"recipient":"`+pk.Hex()+`","message":"tracked","receipts":true}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("receipts send status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode receipts response: %v (%q)", err, rr.Body.String())
	}
	if !resp.OK || resp.ID == "" {
		t.Errorf("receipts response = %+v, want ok plus a wire id the UI can track", resp)
	}
	select {
	case raw := <-cc.frames:
		if !strings.Contains(string(raw), resp.ID) {
			t.Errorf("wire frame %q should carry the returned id %q", raw, resp.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the wire on a receipts send")
	}
}

// TestMessageHandler_WaitTimesOut — a --wait send against a peer that never
// acks must report 504 with acked:false rather than claiming delivery.
func TestMessageHandler_WaitTimesOut(t *testing.T) {
	withLifecycleEnv(t)
	cc := newCapturingClient()
	chatCtrl = dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
	})

	h := messageHandler(context.Background())
	pk, _ := cipher.GenerateKeyPair()

	rr := httptest.NewRecorder()
	// wait_ms=1 clamps up to chatAckTimeoutFloor (100ms), so this is quick.
	h(rr, httptest.NewRequest(http.MethodPost, "/message",
		strings.NewReader(`{"recipient":"`+pk.Hex()+`","message":"anyone there","wait_ms":1}`)))

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504; body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Acked  bool   `json:"acked"`
		Reason string `json:"reason"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%q)", err, rr.Body.String())
	}
	if resp.Acked || resp.Reason != "timeout" {
		t.Errorf("response = %+v, want acked:false reason:timeout", resp)
	}
	if resp.ID == "" {
		t.Error("a timed-out wait send should still report the id it used")
	}
}

// --- onChatEvent ------------------------------------------------------------

func TestOnChatEvent_SurfacesBothDirections(t *testing.T) {
	withLifecycleEnv(t)
	peer, _ := cipher.GenerateKeyPair()

	sub, unsub := hub.subscribeEvents(nil, 0)
	defer unsub()

	onChatEvent(dm.Event{ID: "in-1", Dir: "in", Peer: peer.Hex(), Network: "skynet", Text: "hi"})
	onChatEvent(dm.Event{Dir: "out", Peer: peer.Hex(), Network: "dmsg", Text: "yo", ReplyTo: "in-1"})

	var got []chatEvent
	for len(got) < 2 {
		select {
		case ev := <-sub:
			got = append(got, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d events surfaced, want 2", len(got))
		}
	}

	in, out := got[0], got[1]
	if in.Dir != "in" || in.From != peer.Hex() || in.To != "" {
		t.Errorf("inbound = dir %q from %q to %q; want in/<peer>/empty", in.Dir, in.From, in.To)
	}
	if in.ID != "in-1" || in.Transport != "skynet" || in.Len != len("hi") {
		t.Errorf("inbound event = %+v", in)
	}
	if out.Dir != "out" || out.To != peer.Hex() {
		t.Errorf("outbound = dir %q to %q; want out/<peer>", out.Dir, out.To)
	}
	if out.ReplyToID != "in-1" {
		t.Errorf("outbound ReplyToID = %q, want the quoted id", out.ReplyToID)
	}
	// An event with no id gets one minted, or the UI cannot address the bubble.
	if out.ID == "" {
		t.Error("an id-less controller event should be assigned a fresh event id")
	}
}

// --- feature-gated mux registration -----------------------------------------

func TestRegisterHandlers_DisabledRegistersNothing(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = false

	mux := http.NewServeMux()
	registerGroupHTTPHandlers(mux)
	registerPresenceHTTPHandlers(mux)
	registerVoiceHTTPHandlers(mux)

	paths := []string{"/group", "/group/join", "/presence", "/voice/call", "/voice/active", "/voice/levels"}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404 — a standalone skychat must not advertise it", p, rr.Code)
			}
		})
	}
}

func TestRegisterHandlers_EnabledRegistersRoutes(t *testing.T) {
	withLifecycleEnv(t)
	resetAuth(t)
	if err := loadSkychatPassword(""); err != nil {
		t.Fatal(err)
	}
	pairEnable = true
	withPairRPCDown(t) // registered but RPC down → 503, which proves the route exists

	mux := http.NewServeMux()
	registerGroupHTTPHandlers(mux)
	registerPresenceHTTPHandlers(mux)
	registerVoiceHTTPHandlers(mux)

	// Every route must resolve to its handler (503 from the RPC guard), never
	// to the mux's 404.
	for _, p := range []string{"/group", "/group/join", "/voice/active", "/voice/incoming", "/voice/levels"} {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
			if rr.Code == http.StatusNotFound {
				t.Errorf("%s was not registered", p)
			}
		})
	}
	// /presence is registered too (it answers without the RPC).
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/presence", nil))
	if rr.Code == http.StatusNotFound {
		t.Error("/presence was not registered")
	}
}

// --- pollers ----------------------------------------------------------------

// pollAPI is a fake visor.API for the two SSE-bridge pollers.
type pollAPI struct {
	visorAPIShim
	mu     sync.Mutex
	pair   []visor.PairMessage
	group  []visor.GroupMessage
	pairN  int
	groupN int
	// polled fires once per poll. stopPairPoller/stopGroupPoller only CANCEL
	// the loop — they do not join it — so a test that restored the globals the
	// goroutine reads (hub, pairPollInterval) right after canceling would race
	// it. Receiving from this channel is a real happens-before edge with
	// everything the goroutine did before that poll, which is what awaitQuiet
	// below relies on.
	polled chan struct{}
}

func newPollAPI() *pollAPI { return &pollAPI{polled: make(chan struct{}, 64)} }

func (p *pollAPI) signal() {
	select {
	case p.polled <- struct{}{}:
	default:
	}
}

// awaitQuiet waits for `n` further polls, then cancels. Each poll after the
// first returns no messages, so the goroutine touches no globals in those
// iterations; receiving their signals therefore orders every hub access the
// goroutine ever makes before the caller's cleanup.
func (p *pollAPI) awaitQuiet(t *testing.T, n int) { //nolint
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-p.polled:
		case <-time.After(3 * time.Second):
			t.Fatal("poller stopped ticking before it went quiet")
		}
	}
}

func (p *pollAPI) PairPoll(time.Time) ([]visor.PairMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pairN++
	out := p.pair
	p.pair = nil // deliver once, like a drained inbox
	p.signal()
	return out, nil
}

func (p *pollAPI) GroupPoll(time.Time) ([]visor.GroupMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.groupN++
	out := p.group
	p.group = nil
	p.signal()
	return out, nil
}

func (p *pollAPI) counts() (pair, group int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pairN, p.groupN
}

func TestPollers_DisabledDoNotRun(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = false
	pairPollInterval = 10 * time.Millisecond
	fake := newPollAPI()
	withFakePairRPC(t, fake)

	startPairPoller(context.Background())
	startGroupPoller(context.Background())
	t.Cleanup(func() { stopPairPoller(); stopGroupPoller() })

	time.Sleep(120 * time.Millisecond)
	if pairN, groupN := fake.counts(); pairN != 0 || groupN != 0 {
		t.Errorf("polls with pairing disabled: pair=%d group=%d, want 0/0", pairN, groupN)
	}
}

func TestPairPoller_BridgesInboundToSSE(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = true
	pairPollInterval = 10 * time.Millisecond

	peer, _ := cipher.GenerateKeyPair()
	fake := newPollAPI()
	fake.pair = []visor.PairMessage{{PeerPK: peer, Text: "from the pair feed", TS: time.Now().UTC()}}
	withFakePairRPC(t, fake)

	raw, unsub := hub.subscribe()
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	startPairPoller(ctx)
	// Order every hub access the goroutine makes before this test's cleanup.
	t.Cleanup(func() { fake.awaitQuiet(t, 2); cancel(); stopPairPoller() })

	got := waitForString(t, raw, 3*time.Second)
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("poller broadcast is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "pair" || m["message"] != "from the pair feed" || m["sender"] != peer.Hex() {
		t.Errorf("bridged envelope = %v, want a pair-channel message from %s", m, peer.Hex()[:8])
	}
}

func TestGroupPoller_BridgesInboundToSSE(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = true
	pairPollInterval = 10 * time.Millisecond

	peer, _ := cipher.GenerateKeyPair()
	fake := newPollAPI()
	fake.group = []visor.GroupMessage{{GroupID: "g-1", SenderPK: peer, Text: "from the group feed", TS: time.Now().UTC()}}
	withFakePairRPC(t, fake)

	raw, unsub := hub.subscribe()
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	startGroupPoller(ctx)
	t.Cleanup(func() { fake.awaitQuiet(t, 2); cancel(); stopGroupPoller() })

	got := waitForString(t, raw, 3*time.Second)
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("poller broadcast is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "group" || m["group_id"] != "g-1" {
		t.Errorf("bridged envelope = %v, want a group-channel message for g-1", m)
	}
}

func TestStopPollers_Idempotent(t *testing.T) {
	withLifecycleEnv(t)
	// Never started — stopping must be a no-op, not a nil-deref.
	stopPairPoller()
	stopGroupPoller()
	stopPairRPCWatchdog()

	pairEnable = true
	pairPollInterval = 10 * time.Millisecond
	fake := newPollAPI()
	withFakePairRPC(t, fake)
	startPairPoller(context.Background())
	startGroupPoller(context.Background())
	// Both tickers must be built (they read pairPollInterval) before the
	// harness restores it.
	fake.awaitQuiet(t, 2)

	stopPairPoller()
	stopPairPoller() // second stop must not panic
	stopGroupPoller()
	stopGroupPoller()
}

// --- visor RPC dial + watchdog ----------------------------------------------

func TestConnectPairRPCLocked_DisabledAndDialFailure(t *testing.T) {
	withLifecycleEnv(t)

	// Pairing off → no dial attempted, nil client.
	pairEnable = false
	pairRPCAddr = "127.0.0.1:1"
	if got := connectPairRPCLocked("test-disabled"); got != nil {
		t.Error("a disabled pairing subsystem must not dial")
	}

	// Enabled but nothing listening → nil, logged, no panic.
	pairEnable = true
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	dead := lis.Addr().String()
	_ = lis.Close() //nolint:errcheck // now guaranteed closed
	pairRPCAddr = dead

	pairRPCMu.Lock()
	prev := pairRPC
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})

	if got := connectPairRPCLocked("test-dial-fail"); got != nil {
		t.Errorf("dialing a closed port should yield a nil client, got %v", got)
	}
	if pairRPCAlive() {
		t.Error("a failed dial must leave no client installed")
	}
}

func TestConnectPairRPCLocked_DialSuccessSwapsClient(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = true

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = lis.Close() }() //nolint:errcheck
	go func() {
		for {
			c, aerr := lis.Accept()
			if aerr != nil {
				return
			}
			go func() { <-time.After(2 * time.Second); _ = c.Close() }() //nolint:errcheck
		}
	}()
	pairRPCAddr = lis.Addr().String()

	pairRPCMu.Lock()
	prev := pairRPC
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})

	first := connectPairRPCLocked("test-dial-ok")
	if first == nil {
		t.Fatal("dialing a live listener should install a client")
	}
	if !pairRPCAlive() {
		t.Error("a successful dial should leave the client installed")
	}

	// Redial swaps a fresh client in (and closes the old conn).
	second := connectPairRPCLocked("test-redial")
	if second == nil {
		t.Fatal("redial should install a client")
	}
	if first == second {
		t.Error("redial should install a NEW client, not reuse the shut-down one")
	}

	// connectPairRPC is the startup wrapper over the same path.
	connectPairRPC()
	if !pairRPCAlive() {
		t.Error("connectPairRPC should leave a live client after a successful dial")
	}
}

func TestPairRPCWatchdog_DisabledIsNoOpAndStopIsIdempotent(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = false

	startPairRPCWatchdog(context.Background())
	if pairRPCWatchdogCancel != nil {
		t.Error("a disabled watchdog must not install a cancel func")
	}
	stopPairRPCWatchdog()

	pairEnable = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startPairRPCWatchdog(ctx)
	if pairRPCWatchdogCancel == nil {
		t.Fatal("an enabled watchdog should install a cancel func")
	}
	stopPairRPCWatchdog()
	if pairRPCWatchdogCancel != nil {
		t.Error("stop should clear the cancel func so a later stop is a no-op")
	}
	stopPairRPCWatchdog() // must not panic
}

// --- presence probe ---------------------------------------------------------

// pingAPI is a fake visor.API for the dmsg-ping presence probe.
type pingAPI struct {
	visorAPIShim
	dialErr error
	pingErr error
	rtt     time.Duration
	stopped int
}

func (p *pingAPI) DialDmsgPing(cipher.PubKey) error { return p.dialErr }
func (p *pingAPI) StopDmsgPing(cipher.PubKey) error { p.stopped++; return nil }
func (p *pingAPI) DmsgPingOnce(visor.PingConfig) (time.Duration, error) {
	return p.rtt, p.pingErr
}

func TestProbePeerViaVisor(t *testing.T) {
	withLifecycleEnv(t)
	pk, _ := cipher.GenerateKeyPair()

	// RPC down → offline, and no ping is attempted.
	withPairRPCDown(t)
	if got := probePeerViaVisor(pk); got.Online {
		t.Error("with the visor RPC down the peer cannot be reported online")
	}

	// Dial fails (peer unreachable) → offline.
	dialFail := &pingAPI{dialErr: errors.New("no route")}
	withFakePairRPC(t, dialFail)
	if got := probePeerViaVisor(pk); got.Online {
		t.Error("a failed dmsg dial means offline")
	}
	if dialFail.stopped != 0 {
		t.Error("no StopDmsgPing should be issued for a dial that never succeeded")
	}

	// Dial succeeds but the ping does not → online with no RTT. Reaching the
	// peer at all is the liveness signal; the RTT is a nice-to-have.
	pingFail := &pingAPI{pingErr: errors.New("timeout")}
	withFakePairRPC(t, pingFail)
	got := probePeerViaVisor(pk)
	if !got.Online {
		t.Error("a successful dial means online even when the ping itself fails")
	}
	if got.RTTMs != 0 {
		t.Errorf("RTTMs = %d, want 0 when the ping failed", got.RTTMs)
	}
	if pingFail.stopped != 1 {
		t.Errorf("StopDmsgPing called %d times, want 1 — a successful dial must be torn down", pingFail.stopped)
	}

	// Full success → online with the reported RTT.
	ok := &pingAPI{rtt: 42 * time.Millisecond}
	withFakePairRPC(t, ok)
	got = probePeerViaVisor(pk)
	if !got.Online || got.RTTMs != 42 {
		t.Errorf("probe = %+v, want online with RTTMs 42", got)
	}
	if got.At == 0 {
		t.Error("the probe should stamp when it ran")
	}

	// A nonsense RTT from the visor is replaced by our own measurement rather
	// than surfaced as a negative or absurd number.
	weird := &pingAPI{rtt: -5 * time.Second}
	withFakePairRPC(t, weird)
	got = probePeerViaVisor(pk)
	if got.RTTMs < 0 {
		t.Errorf("RTTMs = %d, want a self-timed non-negative value", got.RTTMs)
	}
}

func TestStartPresenceLoop_DisabledIsNoOp(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startPresenceLoop(ctx) // must return immediately without spawning a sweeper

	pairEnable = true
	startPresenceLoop(ctx) // spawns; canceling the ctx unwinds it
}

// --- misc lifecycle ---------------------------------------------------------

func TestLogOSNotifyStartup(t *testing.T) {
	withLifecycleEnv(t)
	// Read the package-wide recorder rather than swapping appLog: background
	// loops from earlier tests can still be calling it, and assigning the global
	// here would race them (see appLogRecorder in osnotify_test.go).
	mark := testAppLog.count()

	osNotify = false
	logOSNotifyStartup()
	if got := testAppLog.since(mark); len(got) != 0 {
		t.Errorf("nothing should be logged when notifications are off, got %v", got)
	}

	mark = testAppLog.count()
	osNotify = true
	logOSNotifyStartup()

	var found bool
	for _, l := range testAppLog.since(mark) {
		if strings.Contains(l, "Host-OS desktop notifications") {
			found = true
		}
	}
	if !found {
		t.Errorf("startup should log a line about host-OS notifications; got %v", testAppLog.since(mark))
	}
}

func TestSendReadReceipt(t *testing.T) {
	withLifecycleEnv(t)
	peer, _ := cipher.GenerateKeyPair()

	chatCtrl = nil
	if err := sendReadReceipt(context.Background(), peer, "m-1"); err == nil {
		t.Error("sending a receipt with no controller should error, not nil-deref")
	}

	cc := newCapturingClient()
	chatCtrl = dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
	})

	if err := sendReadReceipt(context.Background(), peer, "m-1"); err != nil {
		t.Fatalf("sendReadReceipt: %v", err)
	}
	select {
	case raw := <-cc.frames:
		var env message.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("receipt is not an envelope: %v (%q)", err, raw)
		}
		if env.Type != message.TypeRead || env.ID != "m-1" {
			t.Errorf("receipt = %+v, want a chat-read for m-1", env)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no read receipt reached the wire")
	}
}
