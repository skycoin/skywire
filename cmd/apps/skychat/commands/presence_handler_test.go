// Package commands cmd/apps/skychat/commands/presence_handler_test.go
//
// Drives the REAL presenceHandler past its visor-RPC gate, and covers the
// eviction arm of presenceStore.record.
//
// presence_test.go's shape tests predate the fake-RPC harness, so they inline
// the handler's post-gate logic rather than calling it — which exercises a copy
// of the code instead of the code. With withFakePairRPC available the handler
// itself is reachable, so these call it directly.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// withTestPresence swaps the package presence store for one with an injected
// prober, and installs a fake visor RPC so the handler's gate opens.
func withTestPresence(t *testing.T, probe func(cipher.PubKey) peerPresence) {
	t.Helper()
	withFakePairRPC(t, &pairHandlersAPI{}) // any non-nil client opens the gate
	orig := presence
	presence = newTestPresence(probe)
	t.Cleanup(func() { presence = orig })
}

func callPresence(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	presenceHandler()(rr, httptest.NewRequest(method, "/presence", strings.NewReader(body)))
	return rr
}

func TestPresenceHandler_PostReturnsSnapshot(t *testing.T) {
	pks := testPKs(t, 2)
	withTestPresence(t, func(pk cipher.PubKey) peerPresence {
		return peerPresence{Online: pk == pks[0], RTTMs: 7, At: time.Now().Unix()}
	})

	rr := callPresence(t, http.MethodPost,
		`{"pks":["`+pks[0].Hex()+`","`+pks[1].Hex()+`"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}

	var res struct {
		Peers     map[string]peerPresence `json:"peers"`
		RefreshMS int64                   `json:"refresh_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v (%q)", err, rr.Body.String())
	}
	if res.RefreshMS != presenceRefresh.Milliseconds() {
		t.Errorf("refresh_ms = %d, want %d — the UI reads its poll cadence from here",
			res.RefreshMS, presenceRefresh.Milliseconds())
	}

	// POSTing a list also installs it as the watch set, and a NEW contact
	// triggers an immediate sweep rather than waiting out the refresh tick.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(presence.watchSet()) != 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := presence.watchSet(); len(got) != 2 {
		t.Errorf("watch set = %d entries, want 2", len(got))
	}
}

func TestPresenceHandler_GetUsesExistingWatchSet(t *testing.T) {
	pks := testPKs(t, 2)
	withTestPresence(t, func(pk cipher.PubKey) peerPresence {
		return peerPresence{Online: true, RTTMs: 3, At: time.Now().Unix()}
	})
	presence.setWatch(pks)
	presence.sweep(t.Context())

	rr := callPresence(t, http.MethodGet, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	var res struct {
		Peers map[string]peerPresence `json:"peers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Peers) != 2 {
		t.Errorf("GET returned %d peers, want the 2 in the watch set", len(res.Peers))
	}
}

func TestPresenceHandler_RejectsBadInput(t *testing.T) {
	withTestPresence(t, func(cipher.PubKey) peerPresence { return peerPresence{} })

	if rr := callPresence(t, http.MethodPost, "{not json"); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed body: status = %d, want 400", rr.Code)
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		if rr := callPresence(t, m, ""); rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", m, rr.Code)
		}
	}
}

// TestPresenceStore_RecordEvictsUnwatched covers record's pruning arm: the seen
// map tracks the CONTACT LIST, not every peer ever probed, so a long-running
// visor doesn't accumulate entries for peers the operator removed.
func TestPresenceStore_RecordEvictsUnwatched(t *testing.T) {
	p := newTestPresence(func(cipher.PubKey) peerPresence { return peerPresence{} })

	watched := testPKs(t, 3)
	p.setWatch(watched)

	// Record far more than the eviction threshold (presenceMaxWatch*2).
	for _, pk := range testPKs(t, presenceMaxWatch*2+5) {
		p.record(pk, peerPresence{Online: true, At: time.Now().Unix()})
	}
	// Then record the watched ones so they survive the prune.
	for _, pk := range watched {
		p.record(pk, peerPresence{Online: true, At: time.Now().Unix()})
	}

	p.mu.Lock()
	n := len(p.seen)
	_, keptFirst := p.seen[watched[0]]
	p.mu.Unlock()

	if n > presenceMaxWatch*2 {
		t.Errorf("seen holds %d entries, want it pruned to at most %d", n, presenceMaxWatch*2)
	}
	if !keptFirst {
		t.Error("pruning must keep the peers still on the watch set")
	}
}
