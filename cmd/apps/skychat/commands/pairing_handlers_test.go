// Package commands cmd/apps/skychat/commands/pairing_handlers_test.go
//
// Unit coverage for the /pair HTTP surface that pair_inbox_handler_test.go
// left untested: pairRootHandler (add/list), pairItemHandler (remove/send),
// pairInvitesListHandler + pairInvitesItemHandler (the accept/decline
// consent flow), sendPairControl, and registerPairHTTPHandlers' route
// precedence.
//
// Two seams make this hermetic:
//
//   - the visor RPC is a fake visor.API installed via withFakePairRPC
//     (from pair_inbox_handler_test.go), recording every pair mutation;
//   - the wire is a fake dm.Client whose Dial hands back one end of a
//     net.Pipe, so the control frames sendPairControl emits can be read
//     back and asserted rather than inferred.
//
// The best-effort contracts get explicit tests, because they are the ones a
// refactor is most likely to "tidy" into strictness: a POST /pair still
// succeeds when the invite can't be delivered (the pair record is kept so
// both sides converge later), and an accept still succeeds when
// PairMarkActive fails. Conversely, a FAILED PairAdd on accept must leave
// the invite pending so the user can retry.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/message"
	"github.com/skycoin/skywire/pkg/visor"
)

// --- fakes ------------------------------------------------------------------

// pairHandlersAPI is a fake visor.API recording the pair mutations the /pair
// handlers drive through the RPC, with per-method error injection. Only the
// methods these handlers call are implemented; anything else panics via the
// nil-embedded interface, which is the point — an unexpected RPC call should
// fail loudly rather than silently no-op.
type pairHandlersAPI struct {
	visorAPIShim

	mu           sync.Mutex
	added        []cipher.PubKey
	removed      []cipher.PubKey
	markedActive []cipher.PubKey
	sent         []pairSentMsg
	deleted      []pairDeletedMsg
	list         []visor.PairInfo

	addErr        error
	removeErr     error
	markActiveErr error
	sendErr       error
	deleteErr     error
	listErr       error
}

type pairSentMsg struct {
	peer cipher.PubKey
	text string
}

type pairDeletedMsg struct {
	peer cipher.PubKey
	id   string
}

func (a *pairHandlersAPI) PairAdd(pk cipher.PubKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.addErr != nil {
		return a.addErr
	}
	a.added = append(a.added, pk)
	return nil
}

func (a *pairHandlersAPI) PairRemove(pk cipher.PubKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.removeErr != nil {
		return a.removeErr
	}
	a.removed = append(a.removed, pk)
	return nil
}

func (a *pairHandlersAPI) PairMarkActive(pk cipher.PubKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.markActiveErr != nil {
		return a.markActiveErr
	}
	a.markedActive = append(a.markedActive, pk)
	return nil
}

func (a *pairHandlersAPI) PairSend(pk cipher.PubKey, text string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sendErr != nil {
		return "", a.sendErr
	}
	a.sent = append(a.sent, pairSentMsg{peer: pk, text: text})
	return "pair-msg-1", nil
}

func (a *pairHandlersAPI) PairDelete(pk cipher.PubKey, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.deleteErr != nil {
		return a.deleteErr
	}
	a.deleted = append(a.deleted, pairDeletedMsg{peer: pk, id: id})
	return nil
}

func (a *pairHandlersAPI) PairList() ([]visor.PairInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.list, a.listErr
}

func (a *pairHandlersAPI) PairPoll(time.Time) ([]visor.PairMessage, error) { return nil, nil }

func (a *pairHandlersAPI) snapshot() ([]cipher.PubKey, []cipher.PubKey, []cipher.PubKey, []pairSentMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.added, a.removed, a.markedActive, a.sent
}

// capturingClient is a dm.Client whose Dial returns the local end of a
// net.Pipe; a goroutine drains the peer end and publishes each decoded frame
// on `frames`. That makes an outbound pair-control frame observable without a
// mesh. Listen is never reached — the tests drive handlers directly and never
// Start the controller.
type capturingClient struct {
	mu      sync.Mutex
	closers []io.Closer
	dialed  []appnet.Addr
	dialErr error

	frames chan []byte
}

func newCapturingClient() *capturingClient {
	return &capturingClient{frames: make(chan []byte, 16)}
}

func (c *capturingClient) Listen(appnet.Type, routing.Port) (net.Listener, error) {
	return nil, errors.New("capturingClient: Listen not supported")
}

func (c *capturingClient) Dial(addr appnet.Addr) (net.Conn, error) {
	c.mu.Lock()
	c.dialed = append(c.dialed, addr)
	derr := c.dialErr
	c.mu.Unlock()
	if derr != nil {
		return nil, derr
	}

	local, remote := net.Pipe()
	c.mu.Lock()
	c.closers = append(c.closers, local, remote)
	c.mu.Unlock()

	// net.Pipe is unbuffered: without an active reader the app's write would
	// block until its deadline and the send would look like a failure.
	go func() {
		for {
			f, err := message.ReadFrame(remote)
			if err != nil {
				return
			}
			select {
			case c.frames <- f:
			default: // never wedge the reader on a test that stopped listening
			}
		}
	}()
	return local, nil
}

// setDialErr makes every subsequent Dial fail, simulating an offline peer.
func (c *capturingClient) setDialErr(err error) {
	c.mu.Lock()
	c.dialErr = err
	c.mu.Unlock()
}

func (c *capturingClient) dialCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.dialed)
}

func (c *capturingClient) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cl := range c.closers {
		_ = cl.Close() //nolint:errcheck
	}
	c.closers = nil
}

// --- harness ----------------------------------------------------------------

// withPairHandlerEnv turns pairing on, gives the test a private SSE hub, a
// fake visor RPC, and a chatCtrl wired to a capturing transport. Every global
// it touches is restored on cleanup.
func withPairHandlerEnv(t *testing.T, fake visor.API) *capturingClient {
	t.Helper()
	clearPending()

	origHub, origEnable, origCtrl := hub, pairEnable, chatCtrl
	hub = newSSEHub()
	pairEnable = true

	cc := newCapturingClient()
	chatCtrl = dm.New(dm.Config{
		Client:   cc,
		Networks: []appnet.Type{appnet.TypeSkynet},
	})

	withFakePairRPC(t, fake) // sets appLog, swaps pairRPC, restores on cleanup

	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
		hub, pairEnable, chatCtrl = origHub, origEnable, origCtrl
		clearPending()
	})
	return cc
}

// withPairRPCDown drops the RPC client so the handlers' 503 guard fires.
func withPairRPCDown(t *testing.T) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	pairRPCMu.Lock()
	prev := pairRPC
	pairRPC = nil
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})
}

// wantFrame reads one control frame off the wire and returns its decoded type.
func wantFrame(t *testing.T, cc *capturingClient) string {
	t.Helper()
	select {
	case raw := <-cc.frames:
		var env pairMsg
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("control frame is not a pair envelope: %v (raw %q)", err, raw)
		}
		return env.Type
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a pair-control frame on the wire")
		return ""
	}
}

// wantNoFrame asserts nothing was put on the wire.
func wantNoFrame(t *testing.T, cc *capturingClient) {
	t.Helper()
	select {
	case raw := <-cc.frames:
		t.Errorf("unexpected control frame on the wire: %q", raw)
	case <-time.After(150 * time.Millisecond):
	}
}

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// --- pairRootHandler --------------------------------------------------------

func TestPairRootHandler_GetListsPairs(t *testing.T) {
	peer := mustPK(t)
	fake := &pairHandlersAPI{list: []visor.PairInfo{
		{PeerPK: peer, Port: 4242, EstablishedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
	}}
	withPairHandlerEnv(t, fake)

	rr := httptest.NewRecorder()
	pairRootHandler(context.Background())(rr, httptest.NewRequest(http.MethodGet, "/pair", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got []visor.PairInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %q)", err, rr.Body.String())
	}
	if len(got) != 1 || got[0].PeerPK != peer || got[0].Port != 4242 {
		t.Errorf("got = %+v, want one entry for %s:4242", got, peer.Hex())
	}
}

func TestPairRootHandler_GetListErrorReturns500(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{listErr: errors.New("list boom")})

	rr := httptest.NewRecorder()
	pairRootHandler(context.Background())(rr, httptest.NewRequest(http.MethodGet, "/pair", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%q", rr.Code, rr.Body.String())
	}
}

func TestPairRootHandler_PostAddsPairAndSendsInvite(t *testing.T) {
	fake := &pairHandlersAPI{}
	cc := withPairHandlerEnv(t, fake)
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pair",
		strings.NewReader(`{"peer_pk":"`+peer.Hex()+`"}`))
	pairRootHandler(context.Background())(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}
	added, _, _, _ := fake.snapshot()
	if len(added) != 1 || added[0] != peer {
		t.Errorf("PairAdd calls = %v, want one for %s", added, peer.Hex())
	}
	if got := wantFrame(t, cc); got != pairTypeInvite {
		t.Errorf("wire frame type = %q, want %q", got, pairTypeInvite)
	}
}

func TestPairRootHandler_PostSucceedsWhenInviteUndeliverable(t *testing.T) {
	// Documented contract: the pair record is kept even when the peer is
	// unreachable, so both sides converge on next contact. A refactor that
	// made the invite send fatal would strand the operator.
	fake := &pairHandlersAPI{}
	cc := withPairHandlerEnv(t, fake)
	cc.setDialErr(errors.New("peer offline"))
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pair",
		strings.NewReader(`{"peer_pk":"`+peer.Hex()+`"}`))
	pairRootHandler(context.Background())(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 despite the undeliverable invite; body=%q", rr.Code, rr.Body.String())
	}
	added, _, _, _ := fake.snapshot()
	if len(added) != 1 || added[0] != peer {
		t.Errorf("PairAdd calls = %v, want the record kept for %s", added, peer.Hex())
	}
	if cc.dialCount() == 0 {
		t.Error("expected at least one dial attempt for the invite")
	}
}

func TestPairRootHandler_PostPairAddErrorReturns500(t *testing.T) {
	fake := &pairHandlersAPI{addErr: errors.New("add boom")}
	cc := withPairHandlerEnv(t, fake)
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pair",
		strings.NewReader(`{"peer_pk":"`+peer.Hex()+`"}`))
	pairRootHandler(context.Background())(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%q", rr.Code, rr.Body.String())
	}
	// No local record → no invite should go out, or the peer would see an
	// invite for a pair this side doesn't have.
	wantNoFrame(t, cc)
}

func TestPairRootHandler_PostRejectsBadInput(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})

	cases := []struct{ name, body string }{
		{"malformed json", `{"peer_pk":`},
		{"empty body", ``},
		{"non-hex pk", `{"peer_pk":"not-a-pk"}`},
		{"missing pk", `{}`},
		{"truncated pk", `{"peer_pk":"0281a1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/pair", strings.NewReader(c.body))
			pairRootHandler(context.Background())(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPairRootHandler_RejectsOtherMethods(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(m, func(t *testing.T) {
			rr := httptest.NewRecorder()
			pairRootHandler(context.Background())(rr, httptest.NewRequest(m, "/pair", nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rr.Code)
			}
		})
	}
}

func TestPairRootHandler_RPCUnavailableReturns503(t *testing.T) {
	withPairRPCDown(t)
	rr := httptest.NewRecorder()
	pairRootHandler(context.Background())(rr, httptest.NewRequest(http.MethodGet, "/pair", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// --- pairItemHandler --------------------------------------------------------

func TestPairItemHandler_DeleteRemovesPair(t *testing.T) {
	fake := &pairHandlersAPI{}
	withPairHandlerEnv(t, fake)
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	pairItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodDelete, "/pair/"+peer.Hex(), nil))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}
	_, removed, _, _ := fake.snapshot()
	if len(removed) != 1 || removed[0] != peer {
		t.Errorf("PairRemove calls = %v, want one for %s", removed, peer.Hex())
	}
}

func TestPairItemHandler_DeleteErrorReturns500(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{removeErr: errors.New("remove boom")})
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	pairItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodDelete, "/pair/"+peer.Hex(), nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%q", rr.Code, rr.Body.String())
	}
}

func TestPairItemHandler_PostMessageSendsViaCXO(t *testing.T) {
	fake := &pairHandlersAPI{}
	withPairHandlerEnv(t, fake)
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pair/"+peer.Hex()+"/message",
		strings.NewReader(`{"text":"hello over the feed"}`))
	pairItemHandler(context.Background())(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	_, _, _, sent := fake.snapshot()
	if len(sent) != 1 || sent[0].peer != peer || sent[0].text != "hello over the feed" {
		t.Errorf("PairSend calls = %+v, want one {%s, %q}", sent, peer.Hex(), "hello over the feed")
	}
	// The reply must carry the feed id — the browser needs it to name this
	// bubble for a later delete-for-everyone.
	var body struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, rr.Body.String())
	}
	if !body.OK || body.ID != "pair-msg-1" {
		t.Errorf("response = %+v, want {ok:true id:pair-msg-1}", body)
	}
}

func TestPairItemHandler_PostMessageErrorReturns500(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{sendErr: errors.New("send boom")})
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pair/"+peer.Hex()+"/message",
		strings.NewReader(`{"text":"hi"}`))
	pairItemHandler(context.Background())(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%q", rr.Code, rr.Body.String())
	}
}

func TestPairItemHandler_PostMessageBadBodyReturns400(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pair/"+peer.Hex()+"/message",
		strings.NewReader(`{"text":`))
	pairItemHandler(context.Background())(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%q", rr.Code, rr.Body.String())
	}
}

func TestPairItemHandler_RejectsBadPaths(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)

	cases := []struct {
		name, method, path string
		want               int
	}{
		{"missing pk", http.MethodDelete, "/pair/", http.StatusBadRequest},
		{"non-hex pk", http.MethodDelete, "/pair/not-a-pk", http.StatusBadRequest},
		{"truncated pk", http.MethodDelete, "/pair/0281a1", http.StatusBadRequest},
		{"get on item", http.MethodGet, "/pair/" + peer.Hex(), http.StatusMethodNotAllowed},
		{"post without /message", http.MethodPost, "/pair/" + peer.Hex(), http.StatusMethodNotAllowed},
		// DELETE /message is a real route now (retract-for-everyone); with no
		// ?id it's a bad request, not an unknown method. Covered in full by
		// TestPairItemHandler_DeleteMessage* in delete_persistence_test.go.
		{"delete on /message without id", http.MethodDelete, "/pair/" + peer.Hex() + "/message", http.StatusBadRequest},
		{"put on /message", http.MethodPut, "/pair/" + peer.Hex() + "/message", http.StatusMethodNotAllowed},
		{"unknown subresource", http.MethodPost, "/pair/" + peer.Hex() + "/bogus", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			pairItemHandler(context.Background())(rr, httptest.NewRequest(c.method, c.path, nil))
			if rr.Code != c.want {
				t.Errorf("%s %s: status = %d, want %d; body=%q", c.method, c.path, rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

func TestPairItemHandler_RPCUnavailableReturns503(t *testing.T) {
	withPairRPCDown(t)
	peer := mustPK(t)
	rr := httptest.NewRecorder()
	pairItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodDelete, "/pair/"+peer.Hex(), nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// --- pairInvitesListHandler -------------------------------------------------

func TestPairInvitesListHandler_ReturnsPendingInvites(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesListHandler()(rr, httptest.NewRequest(http.MethodGet, "/pair/invites", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got []pendingInvite
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %q)", err, rr.Body.String())
	}
	if len(got) != 1 || got[0].PeerPK != peer {
		t.Fatalf("got = %+v, want one pending invite from %s", got, peer.Hex())
	}
	if got[0].ReceivedAt.IsZero() {
		t.Error("pending invite should carry a received_at timestamp")
	}
}

func TestPairInvitesListHandler_EmptyReturnsJSONArray(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})

	rr := httptest.NewRecorder()
	pairInvitesListHandler()(rr, httptest.NewRequest(http.MethodGet, "/pair/invites", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// pendingList pre-allocates, so an empty set must marshal as [] — a UI
	// doing `.forEach` on the response would break on null.
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestPairInvitesListHandler_RejectsNonGet(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	for _, m := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			rr := httptest.NewRecorder()
			pairInvitesListHandler()(rr, httptest.NewRequest(m, "/pair/invites", nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rr.Code)
			}
		})
	}
}

func TestPairInvitesListHandler_RPCUnavailableReturns503(t *testing.T) {
	withPairRPCDown(t)
	rr := httptest.NewRecorder()
	pairInvitesListHandler()(rr, httptest.NewRequest(http.MethodGet, "/pair/invites", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// --- pairInvitesItemHandler -------------------------------------------------

func TestPairInvitesItemHandler_AcceptPairsAndAcks(t *testing.T) {
	fake := &pairHandlersAPI{}
	cc := withPairHandlerEnv(t, fake)
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/accept", nil))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}
	added, _, active, _ := fake.snapshot()
	if len(added) != 1 || added[0] != peer {
		t.Errorf("PairAdd calls = %v, want one for %s", added, peer.Hex())
	}
	if len(active) != 1 || active[0] != peer {
		t.Errorf("PairMarkActive calls = %v, want one for %s — the user consented, "+
			"so this side is active immediately", active, peer.Hex())
	}
	if pendingHas(peer) {
		t.Error("accepted invite should be dropped from the pending set")
	}
	if got := wantFrame(t, cc); got != pairTypeAck {
		t.Errorf("wire frame type = %q, want %q", got, pairTypeAck)
	}
}

func TestPairInvitesItemHandler_AcceptSucceedsWhenMarkActiveFails(t *testing.T) {
	// PairMarkActive is best-effort: the pair record already exists, so a
	// failure here must not undo the accept or 500 the operator.
	fake := &pairHandlersAPI{markActiveErr: errors.New("mark boom")}
	cc := withPairHandlerEnv(t, fake)
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/accept", nil))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}
	if pendingHas(peer) {
		t.Error("invite should still be cleared when PairMarkActive fails")
	}
	if got := wantFrame(t, cc); got != pairTypeAck {
		t.Errorf("wire frame type = %q, want %q", got, pairTypeAck)
	}
}

func TestPairInvitesItemHandler_AcceptPairAddErrorKeepsInvitePending(t *testing.T) {
	// PairAdd is the one call that must be fatal: with no local pair record
	// there is nothing to ack, and dropping the invite would leave the user
	// with no way to retry from the UI.
	fake := &pairHandlersAPI{addErr: errors.New("add boom")}
	cc := withPairHandlerEnv(t, fake)
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/accept", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%q", rr.Code, rr.Body.String())
	}
	if !pendingHas(peer) {
		t.Error("a failed accept must leave the invite pending so the user can retry")
	}
	wantNoFrame(t, cc)
}

func TestPairInvitesItemHandler_DeclineDropsInviteAndNotifiesPeer(t *testing.T) {
	fake := &pairHandlersAPI{}
	cc := withPairHandlerEnv(t, fake)
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/decline", nil))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}
	if pendingHas(peer) {
		t.Error("declined invite should be dropped from the pending set")
	}
	added, _, active, _ := fake.snapshot()
	if len(added) != 0 || len(active) != 0 {
		t.Errorf("decline must not create a pair: added=%v active=%v", added, active)
	}
	if got := wantFrame(t, cc); got != pairTypeDecline {
		t.Errorf("wire frame type = %q, want %q", got, pairTypeDecline)
	}
}

func TestPairInvitesItemHandler_DeclineSucceedsWhenPeerUnreachable(t *testing.T) {
	fake := &pairHandlersAPI{}
	cc := withPairHandlerEnv(t, fake)
	cc.setDialErr(errors.New("peer offline"))
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/decline", nil))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 despite the undeliverable decline; body=%q", rr.Code, rr.Body.String())
	}
	if pendingHas(peer) {
		t.Error("invite should be dropped locally even when the peer can't be told")
	}
}

func TestPairInvitesItemHandler_UnknownPeerReturns404(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t) // never pendingPut

	for _, action := range []string{"accept", "decline"} {
		t.Run(action, func(t *testing.T) {
			rr := httptest.NewRecorder()
			pairInvitesItemHandler(context.Background())(rr,
				httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/"+action, nil))
			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body=%q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPairInvitesItemHandler_UnknownActionReturns400(t *testing.T) {
	// The pending check runs before the action switch, so the invite must
	// exist for the request to reach the 400.
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)
	pendingPut(peer)

	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/maybe", nil))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%q", rr.Code, rr.Body.String())
	}
	if !pendingHas(peer) {
		t.Error("an unknown action must not consume the pending invite")
	}
}

func TestPairInvitesItemHandler_RejectsBadPaths(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)
	pendingPut(peer)

	cases := []struct {
		name, path string
	}{
		{"no action", "/pair/invites/" + peer.Hex()},
		{"empty action", "/pair/invites/" + peer.Hex() + "/"},
		{"empty pk", "/pair/invites//accept"},
		{"nothing at all", "/pair/invites/"},
		{"non-hex pk", "/pair/invites/not-a-pk/accept"},
		{"truncated pk", "/pair/invites/0281a1/accept"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			pairInvitesItemHandler(context.Background())(rr,
				httptest.NewRequest(http.MethodPost, c.path, nil))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400; body=%q", c.path, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPairInvitesItemHandler_RejectsNonPost(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)
	pendingPut(peer)

	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		t.Run(m, func(t *testing.T) {
			rr := httptest.NewRecorder()
			pairInvitesItemHandler(context.Background())(rr,
				httptest.NewRequest(m, "/pair/invites/"+peer.Hex()+"/accept", nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rr.Code)
			}
		})
	}
	if !pendingHas(peer) {
		t.Error("a rejected method must not consume the pending invite")
	}
}

func TestPairInvitesItemHandler_RPCUnavailableReturns503(t *testing.T) {
	withPairRPCDown(t)
	peer := mustPK(t)
	rr := httptest.NewRecorder()
	pairInvitesItemHandler(context.Background())(rr,
		httptest.NewRequest(http.MethodPost, "/pair/invites/"+peer.Hex()+"/accept", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// --- sendPairControl --------------------------------------------------------

func TestSendPairControl_WritesEnvelopeForEachType(t *testing.T) {
	cc := withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)

	for _, want := range []string{pairTypeInvite, pairTypeAck, pairTypeDecline} {
		t.Run(want, func(t *testing.T) {
			if err := sendPairControl(context.Background(), peer, want); err != nil {
				t.Fatalf("sendPairControl(%s) = %v, want nil", want, err)
			}
			raw := <-cc.frames
			var env pairMsg
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("frame is not a pair envelope: %v (raw %q)", err, raw)
			}
			if env.Type != want {
				t.Errorf("frame type = %q, want %q", env.Type, want)
			}
			// from_pk is informational and omitted on the wire — the receiver
			// authenticates via the connection's remote PK instead.
			if env.FromPK != "" {
				t.Errorf("from_pk = %q, want empty (identity comes from the conn)", env.FromPK)
			}
		})
	}
}

func TestSendPairControl_ReturnsErrorWhenUndeliverable(t *testing.T) {
	cc := withPairHandlerEnv(t, &pairHandlersAPI{})
	cc.setDialErr(errors.New("peer offline"))
	peer := mustPK(t)

	if err := sendPairControl(context.Background(), peer, pairTypeInvite); err == nil {
		t.Error("sendPairControl should surface the dial failure so callers can log it")
	}
}

// --- parsePK empty guard ----------------------------------------------------

func TestParsePK_RejectsEmpty(t *testing.T) {
	// cipher.PubKey.UnmarshalText("") is a no-op returning a nil error, so an
	// absent peer_pk would otherwise parse as the all-zero key and reach
	// PairAdd/PairSend/SendRaw as a real peer. Guarded in parsePK so every
	// body-based caller inherits it.
	for _, s := range []string{"", " ", "\t\n"} {
		if pk, err := parsePK(s); err == nil {
			t.Errorf("parsePK(%q) = %s, nil; want an error", s, pk.Hex())
		}
	}
}

// --- registerPairHTTPHandlers ----------------------------------------------

func TestRegisterPairHTTPHandlers_RoutePrecedence(t *testing.T) {
	// /pair/inbox and /pair/invites must beat the /pair/ catchall. Regression
	// pin for #2699: the inbox path once fell through to pairItemHandler,
	// which parsed "inbox" as a peer-PK and answered 400.
	fake := &pairHandlersAPI{}
	withPairHandlerEnv(t, fake)
	resetAuth(t)
	if err := loadSkychatPassword(""); err != nil { // no password → passthrough
		t.Fatal(err)
	}
	peer := mustPK(t)

	mux := http.NewServeMux()
	registerPairHTTPHandlers(context.Background(), mux)

	cases := []struct {
		name, method, path string
		want               int
	}{
		{"inbox not swallowed by catchall", http.MethodGet, "/pair/inbox", http.StatusOK},
		{"invites list", http.MethodGet, "/pair/invites", http.StatusOK},
		{"root list", http.MethodGet, "/pair", http.StatusOK},
		{"item delete", http.MethodDelete, "/pair/" + peer.Hex(), http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(c.method, c.path, nil))
			if rr.Code != c.want {
				t.Errorf("%s %s: status = %d, want %d; body=%q", c.method, c.path, rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

func TestRegisterPairHTTPHandlers_DisabledRegistersNothing(t *testing.T) {
	origEnable := pairEnable
	pairEnable = false
	t.Cleanup(func() { pairEnable = origEnable })

	mux := http.NewServeMux()
	registerPairHTTPHandlers(context.Background(), mux)

	for _, path := range []string{"/pair", "/pair/invites", "/pair/inbox"} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404 (pairing off registers no routes)", path, rr.Code)
			}
		})
	}
}
