// Package commands cmd/apps/skychat/commands/dmstatus_test.go
//
// Unit coverage for the DM message-status plumbing: the dm-status control
// event shape broadcast on /sse, and the /read-receipt handler's validation
// (method, pk, standalone-mode guard).
//
// The stale-conn recovery that used to live here moved into the shared
// controller with the conn cache — see TestController_RequestAck* in
// pkg/skychat/dm.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// withHubAndPairing gives the test a private SSE hub (so broadcasts can be
// read back) and pairing off, restoring both afterwards. Lived in
// handleconn_test.go until the DM read loop moved into pkg/skychat/dm.
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

// waitForString reads one SSE broadcast or fails the test.
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

func TestBroadcastDMStatus_EmitsControlEvent(t *testing.T) {
	withHubAndPairing(t)
	sub, unsub := hub.subscribe()
	defer unsub()

	broadcastDMStatus("m-1", dmStatusReceived, "peerhex")
	got := waitForString(t, sub, 2*time.Second)

	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("decode dm-status: %v (raw %q)", err, got)
	}
	if m["channel"] != "dm-status" || m["id"] != "m-1" || m["status"] != "received" || m["peer"] != "peerhex" {
		t.Errorf("dm-status event = %v", m)
	}
}

func TestBroadcastDMStatus_EmptyIDNoOp(t *testing.T) {
	withHubAndPairing(t)
	sub, unsub := hub.subscribe()
	defer unsub()

	broadcastDMStatus("", dmStatusRead, "peerhex") // must not broadcast
	select {
	case s := <-sub:
		t.Errorf("empty id must not broadcast, got %q", s)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestReadReceiptHandler_Validation(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	h := readReceiptHandler(context.Background())
	pk, _ := cipher.GenerateKeyPair()

	// Non-POST → 405.
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/read-receipt", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code=%d, want 405", rr.Code)
	}

	// Malformed JSON body → 400 (decode error, before pk parsing).
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/read-receipt", strings.NewReader(`{not json`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad body: code=%d, want 400", rr.Code)
	}

	// Bad pk → 400 (parsed before the transport guard).
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/read-receipt", strings.NewReader(`{"pk":"nothex","ids":["a"]}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad pk: code=%d, want 400", rr.Code)
	}

	// Valid pk but standalone (appCl == nil) → 503.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/read-receipt",
		strings.NewReader(`{"pk":"`+pk.Hex()+`","ids":["a","b"]}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("standalone: code=%d, want 503 (body=%q)", rr.Code, rr.Body.String())
	}
}
