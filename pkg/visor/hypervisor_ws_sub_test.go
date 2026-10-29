//go:build !mobile && !js

package visor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skycoin/skywire/pkg/logging"
)

// wsSubTestServer serves /ws over a router whose /api/counter returns the
// current value of n, so a test can change what a subscription sees.
func wsSubTestServer(t *testing.T, n *atomic.Int64) (*websocket.Conn, context.Context) {
	t.Helper()
	old := wsSubMinEvery
	wsSubMinEvery = 20 * time.Millisecond
	t.Cleanup(func() { wsSubMinEvery = old })

	hv := &Hypervisor{logger: logging.MustGetLogger("ws-sub-test")}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/counter", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(strconv.FormatInt(n.Load(), 10))) //nolint:errcheck,gosec
	})
	hv.wsMux.Store(muxRef{h: mux})
	srv := httptest.NewServer(hv.getAPIWebSocket())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() }) //nolint:errcheck
	return c, ctx
}

func wsSend(ctx context.Context, t *testing.T, c *websocket.Conn, req wsRequest) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// wsFrames reads every frame into a channel. A Read whose context ends closes
// the connection in coder/websocket, so waits happen on the channel instead.
func wsFrames(ctx context.Context, t *testing.T, c *websocket.Conn) <-chan wsResponse {
	t.Helper()
	ch := make(chan wsResponse, 16)
	go func() {
		defer close(ch)
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var res wsResponse
			if json.Unmarshal(data, &res) == nil {
				ch <- res
			}
		}
	}()
	return ch
}

// wsNext returns the next frame, or nil when none arrives within wait.
func wsNext(ch <-chan wsResponse, wait time.Duration) *wsResponse {
	select {
	case res, ok := <-ch:
		if !ok {
			return nil
		}
		return &res
	case <-time.After(wait):
		return nil
	}
}

// TestWSSubscribe checks a subscription sends at once, then only on change,
// and stops after unsub.
func TestWSSubscribe(t *testing.T) {
	var n atomic.Int64
	n.Store(1)
	c, ctx := wsSubTestServer(t, &n)
	frames := wsFrames(ctx, t, c)

	wsSend(ctx, t, c, wsRequest{Type: "sub", ID: 9, Path: "/api/counter", EveryMs: 1})
	if res := wsNext(frames, 5*time.Second); res == nil || res.Type != "event" || res.ID != 9 || res.Body != "1" {
		t.Fatalf("first frame = %+v, want event 9 with body 1", res)
	}
	if res := wsNext(frames, 200*time.Millisecond); res != nil {
		t.Fatalf("got %+v while nothing changed, want silence", res)
	}

	n.Store(2)
	if res := wsNext(frames, 5*time.Second); res == nil || res.Body != "2" {
		t.Fatalf("after a change got %+v, want body 2", res)
	}

	wsSend(ctx, t, c, wsRequest{Type: "unsub", ID: 9})
	time.Sleep(100 * time.Millisecond)
	n.Store(3)
	if res := wsNext(frames, 300*time.Millisecond); res != nil {
		t.Fatalf("got %+v after unsub, want silence", res)
	}
}

// TestWSSubscribeRejectsPath checks a subscription is held to the same path
// guard as a request, and says so once rather than every interval.
func TestWSSubscribeRejectsPath(t *testing.T) {
	var n atomic.Int64
	c, ctx := wsSubTestServer(t, &n)
	frames := wsFrames(ctx, t, c)

	wsSend(ctx, t, c, wsRequest{Type: "sub", ID: 4, Path: "/ws", EveryMs: 1})
	if res := wsNext(frames, 5*time.Second); res == nil || res.Status != http.StatusForbidden {
		t.Fatalf("got %+v, want a 403 event", res)
	}
	if res := wsNext(frames, 200*time.Millisecond); res != nil {
		t.Fatalf("got %+v after the rejection, want silence", res)
	}
}
