// Package commands cmd/apps/skychat/commands/pair_inbox_handler_test.go
//
// Unit test for the /pair/inbox HTTP handler added to close the
// shipping gap from #2699: the CLI `cli skychat pair poll` builds
// GET /pair/inbox?limit=N&since=TS but the chat-app mux only had
// /pair, /pair/invites, /pair/invites/, /pair/. The path fell into
// the /pair/{pk} catchall which parsed "inbox" as a peer-PK and
// rejected the request (HTTP 400 "invalid peer-pk").
//
// The handler is intentionally cheap to test: it doesn't open
// connections, mutate state, or talk to the visor's pairing manager
// — it only validates query params, calls PairPoll via the
// reconnect helper, applies the limit window, and JSON-encodes.

package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

// pairPollAPI is a fake visor.API that returns a fixed PairPoll
// result. All other visor.API methods are unimplemented (nil-embed
// would panic); the handler only calls PairPoll.
type pairPollAPI struct {
	visorAPIShim
	msgs []visor.PairMessage
	err  error
	// lastSince captures the `since` arg the handler passed through.
	lastSince time.Time
}

func (p *pairPollAPI) PairPoll(since time.Time) ([]visor.PairMessage, error) {
	p.lastSince = since
	return p.msgs, p.err
}

// withFakePairRPC installs a fake visor.API as the package-level
// pairRPC for the duration of the test. Restores on Cleanup.
func withFakePairRPC(t *testing.T, fake visor.API) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...interface{}) {}
	}
	pairRPCMu.Lock()
	prev := pairRPC
	pairRPC = fake
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})
}

func mkPairMsg(t *testing.T, text string, ts time.Time) visor.PairMessage {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return visor.PairMessage{PeerPK: pk, Text: text, TS: ts}
}

func TestPairInboxHandler_EmptyInboxReturnsEmptyArray(t *testing.T) {
	withFakePairRPC(t, &pairPollAPI{msgs: nil})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox", nil)
	pairInboxHandler()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" && got != "null" {
		t.Errorf("body = %q, want [] or null", got)
	}
}

func TestPairInboxHandler_ReturnsAllMessagesAsJSONArray(t *testing.T) {
	base := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	fake := &pairPollAPI{msgs: []visor.PairMessage{
		mkPairMsg(t, "first", base.Add(1*time.Second)),
		mkPairMsg(t, "second", base.Add(2*time.Second)),
		mkPairMsg(t, "third", base.Add(3*time.Second)),
	}}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox", nil)
	pairInboxHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []visor.PairMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not a JSON array of PairMessage: %v\nbody=%q", err, rr.Body.String())
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	want := []string{"first", "second", "third"}
	for i, m := range got {
		if m.Text != want[i] {
			t.Errorf("got[%d].Text = %q, want %q", i, m.Text, want[i])
		}
	}
}

func TestPairInboxHandler_LimitTruncatesOldestFirst(t *testing.T) {
	// CLI prints oldest-first, so when the inbox window exceeds the
	// caller's limit the handler should drop the OLDEST extras (keep
	// the newest N). This pins the contract so a future refactor that
	// flipped the slice direction is caught.
	base := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	fake := &pairPollAPI{msgs: []visor.PairMessage{
		mkPairMsg(t, "oldest", base.Add(1*time.Second)),
		mkPairMsg(t, "middle", base.Add(2*time.Second)),
		mkPairMsg(t, "newest", base.Add(3*time.Second)),
	}}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox?limit=2", nil)
	pairInboxHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []visor.PairMessage
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Text != "middle" || got[1].Text != "newest" {
		t.Errorf("got = [%q, %q], want [middle, newest]", got[0].Text, got[1].Text)
	}
}

func TestPairInboxHandler_SinceThreadsThroughToVisor(t *testing.T) {
	// The since query param is the operator's primary tally tool — if
	// it doesn't reach PairPoll the caller's "messages since 5m ago"
	// claim is meaningless.
	since := time.Date(2026, 5, 18, 0, 5, 0, 0, time.UTC)
	fake := &pairPollAPI{msgs: nil}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox?since="+since.Format(time.RFC3339), nil)
	pairInboxHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if !fake.lastSince.Equal(since) {
		t.Errorf("PairPoll(since) = %v, want %v", fake.lastSince, since)
	}
}

func TestPairInboxHandler_RejectsBadLimit(t *testing.T) {
	withFakePairRPC(t, &pairPollAPI{})
	cases := []string{"0", "1001", "-1", "abc"}
	for _, c := range cases {
		t.Run("limit="+c, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/pair/inbox?limit="+c, nil)
			pairInboxHandler()(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for limit=%q", rr.Code, c)
			}
		})
	}
}

func TestPairInboxHandler_RejectsBadSince(t *testing.T) {
	withFakePairRPC(t, &pairPollAPI{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox?since=not-a-timestamp", nil)
	pairInboxHandler()(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPairInboxHandler_RejectsNonGet(t *testing.T) {
	withFakePairRPC(t, &pairPollAPI{})
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(m, "/pair/inbox", nil)
			pairInboxHandler()(rr, req)
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rr.Code)
			}
		})
	}
}

func TestPairInboxHandler_RPCUnavailableReturns503(t *testing.T) {
	// Pairing manager went down between handler registration and
	// request — the surface should degrade to 503 rather than panic
	// inside the redial path.
	pairRPCMu.Lock()
	prev := pairRPC
	pairRPC = nil
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		pairRPCMu.Lock()
		pairRPC = prev
		pairRPCMu.Unlock()
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox", nil)
	pairInboxHandler()(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestPairInboxHandler_PairPollErrorReturns500(t *testing.T) {
	withFakePairRPC(t, &pairPollAPI{err: errors.New("inbox boom")})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pair/inbox", nil)
	pairInboxHandler()(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%q", rr.Code, rr.Body.String())
	}
}
