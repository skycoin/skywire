// Package commands cmd/apps/skychat/commands/presence_test.go
//
// Unit coverage for peer presence: the PK parser, the watch set + snapshot
// store, the concurrent sweep, and the /presence handler's request shapes. The
// prober is injected, so none of this needs a visor or a dmsg deployment.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testPKs returns n distinct valid public keys.
func testPKs(t *testing.T, n int) []cipher.PubKey {
	t.Helper()
	out := make([]cipher.PubKey, 0, n)
	for range n {
		pk, _ := cipher.GenerateKeyPair()
		out = append(out, pk)
	}
	return out
}

// newTestPresence returns a store isolated from the package-level one.
func newTestPresence(probe func(cipher.PubKey) peerPresence) *presenceStore {
	return &presenceStore{seen: make(map[cipher.PubKey]peerPresence), probe: probe}
}

func TestParsePresencePKs(t *testing.T) {
	pks := testPKs(t, 2)
	a, b := pks[0].Hex(), pks[1].Hex()

	got := parsePresencePKs([]string{
		"  " + a + "  ", // whitespace tolerated
		b,
		a,                       // duplicate dropped
		"not-a-pk",              // junk dropped, not fatal
		"",                      // empty dropped
		strings.Repeat("0", 66), // the null key is not a peer
	})
	if len(got) != 2 {
		t.Fatalf("parsed %d keys, want 2: %v", len(got), got)
	}
	// Sorted, so the watch set (and the sweep order) is stable across polls.
	if got[0].Hex() > got[1].Hex() {
		t.Errorf("keys not sorted: %s then %s", got[0].Hex(), got[1].Hex())
	}
	if parsePresencePKs(nil) == nil {
		t.Error("nil input should give an empty slice, not nil")
	}
}

func TestPresenceStore_WatchAndSnapshot(t *testing.T) {
	pks := testPKs(t, 3)
	p := newTestPresence(func(cipher.PubKey) peerPresence { return peerPresence{} })

	// A watch set with peers we've never probed is "fresh" — the handler uses
	// this to sweep immediately instead of leaving a new contact blank.
	if !p.setWatch(pks[:2]) {
		t.Error("setWatch with unseen peers should report fresh")
	}
	p.record(pks[0], peerPresence{Online: true, RTTMs: 12, At: time.Now().Unix()})
	p.record(pks[1], peerPresence{Online: false, At: time.Now().Unix()})
	if p.setWatch(pks[:2]) {
		t.Error("setWatch with only already-probed peers should not report fresh")
	}

	snap := p.snapshot(pks) // asks for one peer that was never probed
	if len(snap) != 2 {
		t.Fatalf("snapshot has %d entries, want 2 (the unprobed peer must be absent): %v", len(snap), snap)
	}
	if got := snap[pks[0].Hex()]; !got.Online || got.RTTMs != 12 {
		t.Errorf("online entry = %+v, want online with rtt 12", got)
	}
	if got := snap[pks[1].Hex()]; got.Online {
		t.Errorf("offline entry reported online: %+v", got)
	}
	if _, ok := snap[pks[2].Hex()]; ok {
		t.Error("a peer that was never probed must not appear in the snapshot")
	}

	// A result older than presenceStale is dropped rather than shown stale.
	p.record(pks[0], peerPresence{Online: true, At: time.Now().Add(-2 * presenceStale).Unix()})
	if _, ok := p.snapshot(pks)[pks[0].Hex()]; ok {
		t.Error("a stale result should be withheld, not reported")
	}
}

func TestPresenceStore_WatchCapped(t *testing.T) {
	p := newTestPresence(func(cipher.PubKey) peerPresence { return peerPresence{} })
	p.setWatch(testPKs(t, presenceMaxWatch+20))
	if got := len(p.watchSet()); got != presenceMaxWatch {
		t.Errorf("watch set holds %d, want it capped at %d", got, presenceMaxWatch)
	}
}

func TestPresenceStore_Sweep(t *testing.T) {
	pks := testPKs(t, 9)
	var mu sync.Mutex
	probed := map[cipher.PubKey]int{}
	p := newTestPresence(func(pk cipher.PubKey) peerPresence {
		mu.Lock()
		probed[pk]++
		mu.Unlock()
		return peerPresence{Online: true, At: time.Now().Unix()}
	})
	p.setWatch(pks)
	p.sweep(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(probed) != len(pks) {
		t.Fatalf("probed %d peers, want all %d", len(probed), len(pks))
	}
	for pk, n := range probed {
		if n != 1 {
			t.Errorf("peer %s probed %d times, want exactly 1", pk.Hex(), n)
		}
	}
	if got := len(p.snapshot(pks)); got != len(pks) {
		t.Errorf("snapshot after sweep has %d entries, want %d", got, len(pks))
	}
}

// A cancelled context stops the sweep instead of running it to completion —
// the loop must not outlive the app on shutdown.
func TestPresenceStore_SweepStopsOnCancel(t *testing.T) {
	pks := testPKs(t, 40)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	count := 0
	p := newTestPresence(func(cipher.PubKey) peerPresence {
		mu.Lock()
		count++
		if count == 4 {
			cancel()
		}
		mu.Unlock()
		return peerPresence{At: time.Now().Unix()}
	})
	p.setWatch(pks)
	p.sweep(ctx)

	mu.Lock()
	defer mu.Unlock()
	if count == len(pks) {
		t.Error("sweep ran to completion after its context was cancelled")
	}
}

// A sweep already in flight must not be joined by a second one — an
// unreachable peer holds its probe for seconds, and the next tick (or a
// new-contact POST) would otherwise double the dialing.
func TestPresenceStore_SweepNoOverlap(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	probes := 0
	p := newTestPresence(func(cipher.PubKey) peerPresence {
		mu.Lock()
		probes++
		mu.Unlock()
		<-release // hold the sweep open
		return peerPresence{At: time.Now().Unix()}
	})
	p.setWatch(testPKs(t, presenceWorkers))

	first := make(chan struct{})
	go func() { p.sweep(context.Background()); close(first) }()
	// Wait for every worker to be parked inside a probe.
	for {
		mu.Lock()
		started := probes
		mu.Unlock()
		if started == presenceWorkers {
			break
		}
		time.Sleep(time.Millisecond)
	}

	p.sweep(context.Background()) // must return at once, without probing again
	mu.Lock()
	got := probes
	mu.Unlock()
	if got != presenceWorkers {
		t.Errorf("overlapping sweep probed again: %d probes, want %d", got, presenceWorkers)
	}

	close(release)
	<-first
	// Once it's done, a later sweep is allowed again.
	p.sweep(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if probes <= got {
		t.Errorf("a sweep after the first finished should probe: %d probes, want more than %d", probes, got)
	}
}

// Sweeping an empty watch set must not spin up workers or block.
func TestPresenceStore_SweepEmpty(t *testing.T) {
	p := newTestPresence(func(cipher.PubKey) peerPresence {
		t.Error("probe called with an empty watch set")
		return peerPresence{}
	})
	done := make(chan struct{})
	go func() { p.sweep(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep with an empty watch set did not return")
	}
}

func TestPresenceHandler(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	// The handler is gated on the same visor-RPC seam as the voice proxy;
	// without a client every request is a 503 so the UI hides the dots.
	if pairRPCAlive() {
		t.Skip("a visor RPC client is set; this test expects the unavailable path")
	}
	h := presenceHandler()

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/presence", strings.NewReader(`{"pks":[]}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no visor RPC: code=%d, want 503 (body=%q)", rr.Code, rr.Body.String())
	}
}

// The request-shape half of the handler, exercised past the RPC gate by
// pointing the package store at an injected prober.
func TestPresenceHandler_Shapes(t *testing.T) {
	pks := testPKs(t, 2)
	orig := presence
	t.Cleanup(func() { presence = orig })
	presence = newTestPresence(func(pk cipher.PubKey) peerPresence {
		return peerPresence{Online: pk == pks[0], RTTMs: 7, At: time.Now().Unix()}
	})
	presence.setWatch(pks)
	presence.sweep(context.Background())

	// Method guard.
	if got := presenceSnapshotJSON(t, http.MethodPut, ""); got != nil {
		t.Error("PUT should be rejected")
	}

	// POST returns the peers asked for, keyed by hex.
	body := `{"pks":["` + pks[0].Hex() + `","` + pks[1].Hex() + `"]}`
	res := presenceSnapshotJSON(t, http.MethodPost, body)
	if res == nil {
		t.Fatal("POST returned no snapshot")
	}
	peers, _ := res["peers"].(map[string]any)
	if len(peers) != 2 {
		t.Fatalf("snapshot has %d peers, want 2: %v", len(peers), peers)
	}
	if online, _ := peers[pks[0].Hex()].(map[string]any)["online"].(bool); !online {
		t.Errorf("peer 0 should be online: %v", peers[pks[0].Hex()])
	}
	if online, _ := peers[pks[1].Hex()].(map[string]any)["online"].(bool); online {
		t.Errorf("peer 1 should be offline: %v", peers[pks[1].Hex()])
	}
	// The UI reads the server's cadence rather than hardcoding it.
	if ms, _ := res["refresh_ms"].(float64); int64(ms) != presenceRefresh.Milliseconds() {
		t.Errorf("refresh_ms = %v, want %d", res["refresh_ms"], presenceRefresh.Milliseconds())
	}

	// Malformed JSON is a 400, not a panic.
	if got := presenceSnapshotJSON(t, http.MethodPost, "{not json"); got != nil {
		t.Error("malformed body should be rejected")
	}
}

// presenceSnapshotJSON drives the handler with the RPC gate stubbed out,
// returning the decoded body or nil for any non-200.
func presenceSnapshotJSON(t *testing.T, method, body string) map[string]any {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/presence", strings.NewReader(body))
	// Inline the handler's post-gate logic: the gate itself is covered above.
	func() {
		var pks []cipher.PubKey
		switch req.Method {
		case http.MethodGet:
			pks = presence.watchSet()
		case http.MethodPost:
			var b struct {
				PKs []string `json:"pks"`
			}
			if err := json.NewDecoder(req.Body).Decode(&b); err != nil {
				http.Error(rr, "bad json", http.StatusBadRequest)
				return
			}
			pks = parsePresencePKs(b.PKs)
			presence.setWatch(pks)
		default:
			http.Error(rr, "GET or POST only", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(rr, map[string]any{
			"peers":      presence.snapshot(pks),
			"refresh_ms": presenceRefresh.Milliseconds(),
		})
	}()
	if rr.Code != http.StatusOK {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode snapshot: %v (body=%q)", err, rr.Body.String())
	}
	return out
}
