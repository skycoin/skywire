//go:build !mobile

// Package visor pkg/visor/hypervisor_ws_test.go c3-vis-core
package visor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skycoin/skywire/pkg/logging"
)

// wsTestHypervisor builds the minimum /ws needs: a logger and a router to
// replay into. The stub router echoes what it received so a test can assert
// the replayed request, not just the response.
func wsTestHypervisor(t *testing.T) *Hypervisor {
	t.Helper()
	hv := &Hypervisor{logger: logging.MustGetLogger("ws-test")}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Seen-Cookie", r.Header.Get("Cookie"))
		w.Header().Set("X-Seen-Custom", r.Header.Get("X-Custom"))
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte(`{"method":"` + r.Method + `","query":"` + r.URL.RawQuery + `"}`)) //nolint:errcheck,gosec
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, _ *http.Request) {
		// If a frame ever reaches this, the path guard has failed.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("RECURSED")) //nolint:errcheck,gosec
	})
	hv.wsMux.Store(muxRef{h: mux})
	return hv
}

func TestWSAllowedPath(t *testing.T) {
	ok := []string{"/api", "/api/", "/api/about", "/api/visors?x=1", "/api/a#frag"}
	bad := []string{"/ws", "/", "/pty/abc", "/api/../ws", "/apifoo", "", "//api/x", "/tp-viz/"}

	for _, p := range ok {
		if !wsAllowedPath(p) {
			t.Errorf("wsAllowedPath(%q) = false, want true", p)
		}
	}
	for _, p := range bad {
		if wsAllowedPath(p) {
			t.Errorf("wsAllowedPath(%q) = true, want false", p)
		}
	}
}

// TestWSReplayMatchesHTTP is the contract that makes this a transport rather
// than a second API: a frame produces the same status, body and headers the
// equivalent HTTP request would.
func TestWSReplayMatchesHTTP(t *testing.T) {
	hv := wsTestHypervisor(t)
	up := httptest.NewRequest(http.MethodGet, "/ws", nil)
	up.Header.Set("Cookie", "session=real")

	res := hv.serveWSRequest(context.Background(), up, wsRequest{
		Type: "req", ID: 42, Method: "GET", Path: "/api/echo?a=b",
	})

	if res.ID != 42 || res.Type != "res" {
		t.Fatalf("envelope id/type = %d/%q, want 42/res", res.ID, res.Type)
	}
	if res.Status != http.StatusTeapot {
		t.Errorf("status = %d, want %d (the router's own status must pass through)", res.Status, http.StatusTeapot)
	}
	if !strings.Contains(res.Body, `"query":"a=b"`) {
		t.Errorf("body = %q, want the query string preserved", res.Body)
	}
	if got := res.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// TestWSUsesConnectionCredentials: the frame must not be able to present its
// own Cookie. The connection was authenticated at upgrade; a frame that could
// override that would let one socket act as another session.
func TestWSUsesConnectionCredentials(t *testing.T) {
	hv := wsTestHypervisor(t)
	up := httptest.NewRequest(http.MethodGet, "/ws", nil)
	up.Header.Set("Cookie", "session=real")

	res := hv.serveWSRequest(context.Background(), up, wsRequest{
		Method: "GET", Path: "/api/echo",
		Headers: map[string]string{"Cookie": "session=forged", "X-Custom": "kept"},
	})

	if got := res.Headers["X-Seen-Cookie"]; got != "session=real" {
		t.Errorf("replayed Cookie = %q, want the connection's own (session=real)", got)
	}
	if got := res.Headers["X-Seen-Custom"]; got != "kept" {
		t.Errorf("ordinary header = %q, want it forwarded", got)
	}
}

func TestWSRejectsPathAndMethod(t *testing.T) {
	hv := wsTestHypervisor(t)
	up := httptest.NewRequest(http.MethodGet, "/ws", nil)

	t.Run("a frame cannot reach /ws and recurse", func(t *testing.T) {
		res := hv.serveWSRequest(context.Background(), up, wsRequest{Method: "GET", Path: "/ws"})
		if res.Status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", res.Status)
		}
		if strings.Contains(res.Body, "RECURSED") {
			t.Fatal("the frame reached the /ws handler")
		}
	})

	t.Run("method outside the allowlist", func(t *testing.T) {
		res := hv.serveWSRequest(context.Background(), up, wsRequest{Method: "TRACE", Path: "/api/echo"})
		if res.Status != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", res.Status)
		}
	})

	t.Run("router not ready", func(t *testing.T) {
		bare := &Hypervisor{logger: logging.MustGetLogger("ws-test")}
		res := bare.serveWSRequest(context.Background(), up, wsRequest{Method: "GET", Path: "/api/echo"})
		if res.Status != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", res.Status)
		}
	})
}

// TestWSRejectsCrossOrigin pins the security property that motivated the
// explicit Accept options: a WebSocket upgrade is NOT subject to CORS, so
// without an Origin check any page the operator visits could drive the whole
// hypervisor API with their cookies attached.
func TestWSRejectsCrossOrigin(t *testing.T) {
	hv := wsTestHypervisor(t)
	srv := httptest.NewServer(hv.getAPIWebSocket())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example"}},
	})
	if err == nil {
		t.Fatal("cross-origin upgrade succeeded; the Origin check is not in force")
	}
}

// TestWSRoundTrip drives the real handler over a real socket.
func TestWSRoundTrip(t *testing.T) {
	hv := wsTestHypervisor(t)
	srv := httptest.NewServer(hv.getAPIWebSocket())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }() //nolint:errcheck,gosec

	req, merr := json.Marshal(wsRequest{Type: "req", ID: 7, Method: "GET", Path: "/api/echo"})
	if merr != nil {
		t.Fatalf("marshal: %v", merr)
	}
	if err := c.Write(ctx, websocket.MessageText, req); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var res wsResponse
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.ID != 7 || res.Status != http.StatusTeapot {
		t.Errorf("got id=%d status=%d, want id=7 status=418", res.ID, res.Status)
	}
}

// TestWSMalformedEnvelope: a bad frame must not kill the connection — the UI
// would otherwise lose every in-flight request to one serialization slip.
func TestWSMalformedEnvelope(t *testing.T) {
	hv := wsTestHypervisor(t)
	srv := httptest.NewServer(hv.getAPIWebSocket())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }() //nolint:errcheck,gosec

	if err := c.Write(ctx, websocket.MessageText, []byte("{not json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatalf("no error frame for a malformed envelope: %v", err)
	}

	// The connection is still usable.
	req, merr := json.Marshal(wsRequest{Type: "req", ID: 1, Method: "GET", Path: "/api/echo"})
	if merr != nil {
		t.Fatalf("marshal: %v", merr)
	}
	if err := c.Write(ctx, websocket.MessageText, req); err != nil {
		t.Fatalf("write after malformed frame: %v", err)
	}
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatalf("connection died after a malformed frame: %v", err)
	}
}
