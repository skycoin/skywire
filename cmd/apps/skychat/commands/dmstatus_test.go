// Package commands cmd/apps/skychat/commands/dmstatus_test.go
//
// Unit coverage for the DM message-status plumbing: the dm-status control
// event shape broadcast on /sse, and the /read-receipt handler's validation
// (method, pk, standalone-mode guard).
package commands

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// cacheConn registers a fresh framed conn for pk in the package conns map and
// returns it (+ cleanup), so the stale-conn tests can assert against it.
func cacheConn(t *testing.T, pk cipher.PubKey) *framedConn {
	t.Helper()
	raw, _ := net.Pipe()
	fc := newFramedConn(&tcpDirectConn{Conn: raw, rPK: pk})
	connsMu.Lock()
	if conns == nil { // nil until RunSkychat inits it; lazy-init like startHandleConn
		conns = make(map[cipher.PubKey]*framedConn)
	}
	conns[pk] = fc
	connsMu.Unlock()
	t.Cleanup(func() {
		connsMu.Lock()
		delete(conns, pk)
		connsMu.Unlock()
		_ = raw.Close() //nolint
	})
	return fc
}

func connCached(pk cipher.PubKey) (*framedConn, bool) {
	connsMu.Lock()
	defer connsMu.Unlock()
	c, ok := conns[pk]
	return c, ok
}

func TestDropStaleConn_TimeoutDropsConn(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	pk, _ := cipher.GenerateKeyPair()
	fc := cacheConn(t, pk)

	never := make(chan struct{}) // no ack ever arrives
	dropStaleConn(pk, fc, never, nil, 20*time.Millisecond)

	if _, ok := connCached(pk); ok {
		t.Error("a conn with no returning ack should be dropped after the window")
	}
}

func TestDropStaleConn_AckKeepsConn(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	pk, _ := cipher.GenerateKeyPair()
	fc := cacheConn(t, pk)

	acked := make(chan struct{}, 1)
	acked <- struct{}{}
	dropStaleConn(pk, fc, acked, nil, 5*time.Second) // returns via ack, not timeout

	if got, ok := connCached(pk); !ok || got != fc {
		t.Error("an acked conn must stay cached")
	}
}

func TestDropStaleConn_LeavesReplacedConn(t *testing.T) {
	// If a fresh dial already replaced the cached conn before the window fires,
	// the pointer-eq guard must NOT drop the new conn.
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	pk, _ := cipher.GenerateKeyPair()
	newConn := cacheConn(t, pk) // map now holds the NEW conn
	oldRaw, _ := net.Pipe()
	defer func() { _ = oldRaw.Close() }() //nolint
	oldConn := newFramedConn(&tcpDirectConn{Conn: oldRaw, rPK: pk})

	never := make(chan struct{})
	dropStaleConn(pk, oldConn, never, nil, 20*time.Millisecond) // checks the OLD conn

	if got, ok := connCached(pk); !ok || got != newConn {
		t.Error("must not drop a conn that was already replaced by a fresh dial")
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
