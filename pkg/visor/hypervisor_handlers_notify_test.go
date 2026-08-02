// Package visor — pkg/visor/hypervisor_handlers_notify_test.go: pins the
// notification SSE endpoint's contract with the host app (the Android
// foreground service).
//
// Two of these are structural rather than behavioral, and both guard traps
// that are invisible at compile time: the route must resolve OUTSIDE the /api
// group (whose 30s middleware.Timeout would sever a long-lived stream) while
// leaving the group's own routes intact, and the handler must 503 rather than
// panic when there is no hub — bare-visor Hypervisors are a normal fixture here.
package visor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// nonFlusher is a ResponseWriter that cannot stream, which is the one condition
// the handler must reject up front.
type nonFlusher struct {
	hdr    http.Header
	status int
	body   strings.Builder
}

func (n *nonFlusher) Header() http.Header {
	if n.hdr == nil {
		n.hdr = make(http.Header)
	}
	return n.hdr
}
func (n *nonFlusher) Write(b []byte) (int, error) { return n.body.Write(b) }
func (n *nonFlusher) WriteHeader(status int)      { n.status = status }

func TestGetNotifyStream_NonFlusherIs500(t *testing.T) {
	hv := &Hypervisor{visor: &Visor{notifyHub: NewNotifyHub()}}
	w := &nonFlusher{}
	r := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil)

	hv.getNotifyStream()(w, r)

	if w.status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.status, http.StatusInternalServerError)
	}
}

func TestGetNotifyStream_NoHubIs503(t *testing.T) {
	// getLog's sibling omits this guard and would panic; a Hypervisor with a
	// nil visor is constructed in this package's own tests.
	for name, hv := range map[string]*Hypervisor{
		"nil visor": {},
		"no hub":    {visor: &Visor{}},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil)

			hv.getNotifyStream()(w, r)

			if w.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestGetNotifyStream_StreamsEventAndEndsOnContextCancel(t *testing.T) {
	hub := NewNotifyHub()
	hv := &Hypervisor{visor: &Visor{notifyHub: hub}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		hv.getNotifyStream()(w, r)
	}()

	// Wait for the handler to register before publishing, otherwise the event
	// races ahead of the subscription.
	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed to the hub")
		}
		time.Sleep(time.Millisecond)
	}

	hub.Publish(appserver.NotifyReq{App: "skychat", Title: "alice", Body: "hi", Tag: "dm"})

	// Canceling the request context is how a disconnected client is noticed.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request context was canceled")
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if b := w.Header().Get("X-Accel-Buffering"); b != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", b)
	}
	body := w.Body.String()
	want := `data: {"app":"skychat","title":"alice","body":"hi","tag":"dm"}` + "\n\n"
	if !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}

	// The subscription must be released, or the host-OS sink stays suppressed
	// forever and the desktop goes permanently silent.
	if got := hub.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount = %d after handler returned, want 0", got)
	}
}

// TestNotifyStreamRouting pins the structural decision that the route lives
// OUTSIDE the /api group. Inside it, middleware.Timeout(httpTimeout) puts a 30s
// deadline on the whole request and would sever the stream every 30s — invisible
// in a unit test of the handler alone, and on a live-only feed every event in
// the reconnect gap is simply lost. It also checks the sibling routes still
// resolve, since a static /api/notifications edge at the "/" level sits beside
// the /api subrouter.
func TestNotifyStreamRouting(t *testing.T) {
	config := visorconfig.HypervisorConfig{
		DBPath:     filepath.Join(t.TempDir(), "users.db"),
		EnableAuth: false,
	}
	hv, err := NewHypervisor(config, nil, nil)
	if err != nil {
		t.Fatalf("NewHypervisor: %v", err)
	}
	defer hv.users.Close() //nolint:errcheck // test teardown

	mux := hv.HTTPHandler()

	t.Run("stream route resolves to the handler", func(t *testing.T) {
		// hv.visor is nil here, so reaching the handler means 503 — the point
		// is that it is NOT 404, i.e. the route exists and dispatches.
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil))

		if w.Code == http.StatusNotFound {
			t.Fatal("route did not resolve: got 404")
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d (handler reached, no hub)", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("sibling /api routes still resolve", func(t *testing.T) {
		// A static /api/notifications edge is registered at the "/" level,
		// beside the /api subrouter. Confirm that doesn't shadow the group.
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/ping", nil))

		if w.Code == http.StatusNotFound {
			t.Error("/api/ping stopped resolving after adding the /api/notifications edge")
		}
	})
}

// TestNotifyStreamPlacementRationale pins the chi behavior that dictates where
// the route is registered: a handler inside a group carrying
// middleware.Timeout(httpTimeout) sees a request-context deadline, and one
// registered at the "/" level does not — even when its pattern starts /api.
//
// That deadline is fatal to SSE (the response is torn down at httpTimeout, and
// on a live-only feed every event in the reconnect gap is lost), and it is
// invisible in a handler-level test. Should someone "tidy" the route into the
// /api group, this fails and says why.
func TestNotifyStreamPlacementRationale(t *testing.T) {
	var insideDeadline, outsideDeadline bool

	r := chi.NewRouter()
	r.Route("/", func(r chi.Router) {
		r.Route("/api", func(r chi.Router) {
			r.Use(middleware.Timeout(httpTimeout))
			r.Get("/inside", func(_ http.ResponseWriter, req *http.Request) {
				_, insideDeadline = req.Context().Deadline()
			})
		})
		r.Route("/api/outside", func(r chi.Router) {
			r.Get("/stream", func(_ http.ResponseWriter, req *http.Request) {
				_, outsideDeadline = req.Context().Deadline()
			})
		})
	})

	for _, path := range []string{"/api/inside", "/api/outside/stream"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (route must resolve)", path, w.Code)
		}
	}

	if !insideDeadline {
		t.Error("a route inside the /api group should carry a request deadline; the premise of the placement is gone")
	}
	if outsideDeadline {
		t.Error("a route registered outside the /api group must NOT carry a request deadline")
	}
}

func TestGetNotifyStream_OmitsEmptyTag(t *testing.T) {
	// Tag is optional on the wire; skychat publishes without one.
	hub := NewNotifyHub()
	hv := &Hypervisor{visor: &Visor{notifyHub: hub}}

	ctx, cancel := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		hv.getNotifyStream()(w, r)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed to the hub")
		}
		time.Sleep(time.Millisecond)
	}
	hub.Publish(appserver.NotifyReq{App: "skychat", Title: "alice", Body: "hi"})
	cancel()
	<-done

	if strings.Contains(w.Body.String(), `"tag"`) {
		t.Errorf("body = %q, want no tag field when Tag is empty", w.Body.String())
	}
}
