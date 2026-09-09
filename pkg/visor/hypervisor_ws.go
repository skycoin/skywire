// Package visor pkg/visor/hypervisor_ws.go c3-vis-core
//
// /ws — the hypervisor API as a message transport.
//
// The Angular UI already routes /api through one seam (SkywireHttpBackend,
// "the bottom of the Angular HttpClient pipeline"), which today has four
// backends: same-origin XHR for a visor-served build, a function call into an
// in-tab wasm visor, a dmsg call for the standalone hypervisor, and dmsg frames
// to a remote PK. Three of those are message-oriented; the native one is the
// odd one out, and that asymmetry is what stops the two UI variants converging
// on a single transport.
//
// This endpoint closes that gap WITHOUT a second API. A frame names a method, a
// path and a body; the handler replays it through the hypervisor's OWN router,
// so every route, middleware, permission check and response is byte-identical
// to the HTTP call it stands in for. There is exactly one implementation of the
// API — this is a transport, not a parallel surface.
//
// It is deliberately mounted OUTSIDE the /api group: that group carries
// middleware.Timeout, whose wrapped ResponseWriter is not an http.Hijacker (so
// websocket.Accept fails) and whose 30s deadline would kill a long-lived
// socket. /pty sits outside for the same reason and opts into auth explicitly;
// this follows it.
//
// Wire format is JSON text frames. `type` is carried from the first version so
// server-push ("type":"event") can be added later without breaking the
// request/response contract.
//
//	client → server  {"type":"req","id":7,"method":"GET","path":"/api/about","body":null,"headers":{}}
//	server → client  {"type":"res","id":7,"status":200,"body":"{...}","headers":{"Content-Type":"application/json"}}
//
// Bodies are strings, not base64: the hypervisor API is JSON in and JSON out,
// and the UI's gateway contract already decodes a text body.
//
// wsReadLimit bounds what a client may SEND; responses are not capped, and some
// are large — /api/visors-tree-summary is 8.6 MB on a fleet of nine visors, over
// plain HTTP as much as here. A browser's WebSocket has no read limit so the UI
// is unaffected, but a Go client must raise coder/websocket's 32 KiB default
// (SetReadLimit) or the first big response closes the connection.
package visor

import (
	"context"
	"encoding/json"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

const (
	// wsReadLimit caps a single request envelope. The API's largest bodies are
	// config blobs measured in kilobytes; a megabyte is generous and keeps a
	// misbehaving client from growing the read buffer without bound.
	wsReadLimit = 1 << 20

	// wsMaxInFlight bounds concurrently replayed requests per connection. The UI
	// fans several /api calls out at once, so serializing them would make the
	// socket slower than the XHR it replaces; an unbounded fan-out would let one
	// socket occupy every handler goroutine.
	wsMaxInFlight = 8
)

// muxRef boxes the finished router so it can go through an atomic.Value, which
// panics on inconsistent concrete types.
type muxRef struct{ h http.Handler }

// wsRequest is one client→server frame.
type wsRequest struct {
	Type    string            `json:"type"`
	ID      uint64            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    *string           `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// wsResponse is one server→client frame.
type wsResponse struct {
	Type    string            `json:"type"`
	ID      uint64            `json:"id"`
	Status  int               `json:"status"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers,omitempty"`
}

// wsAllowedMethods is what the hypervisor UI actually issues. An allowlist
// rather than a denylist: this endpoint can reach every /api route, so a method
// nobody uses is a method nobody has thought about.
var wsAllowedMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodDelete: true,
}

// wsStrippedHeaders are never taken from the frame. Credentials come from the
// connection, which was authenticated at upgrade time; hop-by-hop and framing
// headers belong to the real HTTP request and would be nonsense on a replay.
var wsStrippedHeaders = map[string]bool{
	"cookie":                   true,
	"authorization":            true,
	"connection":               true,
	"upgrade":                  true,
	"host":                     true,
	"content-length":           true,
	"transfer-encoding":        true,
	"sec-websocket-key":        true,
	"sec-websocket-version":    true,
	"sec-websocket-protocol":   true,
	"sec-websocket-extensions": true,
}

// wsAllowedPath keeps the transport pointed at the API and nothing else.
// Without it a frame could name /ws and recurse, or walk out of /api entirely.
func wsAllowedPath(p string) bool {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if strings.Contains(p, "..") {
		return false
	}
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

// wsRecorder is a minimal ResponseWriter. net/http/httptest would do, but it
// registers an -httptest.serve flag in init(), which has no business in a
// production binary.
type wsRecorder struct {
	hdr  http.Header
	buf  strings.Builder
	code int
}

func (w *wsRecorder) Header() http.Header {
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}

func (w *wsRecorder) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.buf.Write(b)
}

func (w *wsRecorder) WriteHeader(c int) {
	if w.code == 0 {
		w.code = c
	}
}

func (w *wsRecorder) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// getAPIWebSocket serves /ws.
func (hv *Hypervisor) getAPIWebSocket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// nil options on purpose: coder/websocket then requires the Origin host
		// to equal the Host header. A WebSocket upgrade is NOT subject to CORS
		// and cookies ride along on a cross-origin connection, so skipping this
		// would hand any page the operator visits the entire hypervisor API —
		// the same class of hole as a wildcard Access-Control-Allow-Origin, but
		// covering every route rather than a couple of endpoints. Do not pass
		// InsecureSkipVerify or OriginPatterns{"*"} here.
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			hv.logger.WithError(err).Debug("ws: upgrade rejected")
			return
		}
		defer func() { _ = c.CloseNow() }() //nolint:errcheck
		c.SetReadLimit(wsReadLimit)

		// Own cancelation: r.Context() ends when the handler returns, but the
		// in-flight goroutines below have to be unwound explicitly first.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		var (
			wmu sync.Mutex
			wg  sync.WaitGroup
		)
		sem := make(chan struct{}, wsMaxInFlight)

	readLoop:
		for {
			typ, data, rerr := c.Read(ctx)
			if rerr != nil {
				break
			}
			if typ != websocket.MessageText {
				continue
			}

			var req wsRequest
			if jerr := json.Unmarshal(data, &req); jerr != nil {
				hv.writeWS(ctx, c, &wmu, wsResponse{
					Type: "res", Status: http.StatusBadRequest, Body: "malformed envelope",
				})
				continue
			}

			// Labeled: a bare break here would leave the select, not the read
			// loop, and the handler would keep accepting frames after the
			// connection's context was already canceled.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				break readLoop
			}
			wg.Add(1)
			go func(req wsRequest) {
				defer wg.Done()
				defer func() { <-sem }()
				hv.writeWS(ctx, c, &wmu, hv.serveWSRequest(ctx, r, req))
			}(req)
		}

		cancel()
		wg.Wait()
	}
}

// writeWS serializes one frame onto the connection. A websocket connection
// tolerates exactly one concurrent writer.
func (hv *Hypervisor) writeWS(ctx context.Context, c *websocket.Conn, mu *sync.Mutex, resp wsResponse) {
	if resp.Type == "" {
		resp.Type = "res"
	}
	b, err := json.Marshal(resp)
	if err != nil {
		hv.logger.WithError(err).Warn("ws: marshaling response")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		hv.logger.WithError(err).Debug("ws: write failed")
	}
}

// serveWSRequest replays one frame through the hypervisor's own router.
func (hv *Hypervisor) serveWSRequest(ctx context.Context, up *http.Request, req wsRequest) wsResponse {
	res := wsResponse{Type: "res", ID: req.ID}

	if !wsAllowedMethods[strings.ToUpper(req.Method)] {
		res.Status, res.Body = http.StatusMethodNotAllowed, "method not allowed on this transport"
		return res
	}
	if !wsAllowedPath(req.Path) {
		res.Status, res.Body = http.StatusForbidden, "path must be under /api"
		return res
	}

	ref, ok := hv.wsMux.Load().(muxRef)
	if !ok || ref.h == nil {
		res.Status, res.Body = http.StatusServiceUnavailable, "router not ready"
		return res
	}

	var body io.Reader = http.NoBody
	if req.Body != nil {
		body = strings.NewReader(*req.Body)
	}
	// Drop the upgrade request's chi routing context before replaying. chi's
	// Mux.ServeHTTP reuses an existing *chi.Context when it finds one — without
	// resetting it — so a sub-request built from this handler's context inherits
	// RoutePath "/ws" and every replayed path resolves straight back to this
	// handler. Clearing the key makes chi's type assertion miss and allocate a
	// fresh routing context, which is what a new request should get.
	ctx = context.WithValue(ctx, chi.RouteCtxKey, nil)
	sub, err := http.NewRequestWithContext(ctx, strings.ToUpper(req.Method), req.Path, body)
	if err != nil {
		res.Status, res.Body = http.StatusBadRequest, "bad request line"
		return res
	}

	for k, v := range req.Headers {
		if wsStrippedHeaders[strings.ToLower(k)] {
			continue
		}
		sub.Header.Set(k, v)
	}
	// Credentials come from the authenticated connection, never from the frame.
	if ck := up.Header.Get("Cookie"); ck != "" {
		sub.Header.Set("Cookie", ck)
	}
	sub.Host = up.Host
	sub.RemoteAddr = up.RemoteAddr
	if req.Body != nil && sub.Header.Get("Content-Type") == "" {
		sub.Header.Set("Content-Type", "application/json")
	}

	rec := &wsRecorder{}
	ref.h.ServeHTTP(rec, sub)

	res.Status = rec.status()
	res.Body = rec.buf.String()
	if len(rec.hdr) > 0 {
		res.Headers = make(map[string]string, len(rec.hdr))
		for k := range rec.hdr {
			res.Headers[k] = rec.hdr.Get(k)
		}
	}
	return res
}

// getTransportWS → GET /tp/ws : hands the WebSocket upgrade to the visor's WS
// transport client, which runs its normal accept-side handshake on it. The
// same-origin transport a served desk opens back to this visor (see the route
// comment in hypervisor.go).
func (hv *Hypervisor) getTransportWS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil || hv.visor.tpM == nil {
			http.Error(w, "no visor behind this hypervisor", http.StatusServiceUnavailable)
			return
		}
		h, ok := hv.visor.tpM.HTTPAcceptor(tptypes.WS)
		if !ok {
			http.Error(w, "ws transport not available on this visor", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	}
}
