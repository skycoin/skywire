// Package skysocks pkg/skysocks/client.go c4-app-proxy
package skysocks

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // RFC6455 mandates SHA-1 for the WebSocket accept key
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
)

// Client implement multiplexing proxy client using yamux.
type Client struct {
	appCl    *app.Client
	session  *yamux.Session
	listener net.Listener
	once     sync.Once
	closeC   chan struct{}
}

// NewClient constructs a new Client.
func NewClient(conn net.Conn, appCl *app.Client) (*Client, error) {
	c := &Client{
		appCl:  appCl,
		closeC: make(chan struct{}),
	}

	sessionCfg := yamux.DefaultConfig()
	sessionCfg.EnableKeepAlive = false
	session, err := yamux.Client(conn, sessionCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating client: yamux: %w", err)
	}

	c.session = session

	go c.sessionKeepAliveLoop()

	return c, nil
}

// ListenAndServe start tcp listener on addr and proxies incoming
// connection to a remote proxy server.
func (c *Client) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		if c.appCl != nil {
			c.setAppError(err)
		}
		return fmt.Errorf("listen: %w", err)
	}

	if c.appCl != nil {
		c.appCl.Log().Infof("Listening skysocks client on %s", addr)
	}

	c.listener = l
	go func() {
		<-c.closeC
		l.Close() //nolint:errcheck,gosec
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if c.appCl != nil {
				c.appCl.Log().Errorf("Error accepting: %v", err)
			}
			// Release the yamux session + its keepalive goroutine + the
			// underlying conn on Accept failure, mirroring the session.Open
			// error path below. Without this, a listener that fails
			// independently of an orderly Close (so closeC was never
			// signaled) leaked the whole session. c.close() is sync.Once-
			// guarded, so it's a no-op when shutdown already triggered this.
			c.close()
			return fmt.Errorf("accept: %w", err)
		}

		if c.appCl != nil {
			c.appCl.Log().Debug("Accepted skysocks client")
		}

		stream, err := c.session.Open()
		if err != nil {
			// The mesh route/session to the exit is down (exit restart, all
			// mux legs dropped). Before tearing down for reconnect, serve the
			// waiting browser a branded "building a route over skywire…"
			// interstitial for a plaintext-HTTP request so it retries once the
			// route is back, instead of a bare connection failure. Best-effort
			// and deadline-bounded (see ServeSOCKS5); declined for HTTPS/other
			// ports. Runs in a goroutine that owns conn so the reconnect below
			// isn't delayed by a slow browser. The status.skysocks override
			// keeps the in-process status page reachable even now (exit down)
			// instead of being shadowed by the interstitial — status.skysocks
			// needs no exit stream, and this is exactly when the user wants it.
			go func(bc net.Conn) {
				if serr := proxyinterstitial.ServeSOCKS5(bc, proxyinterstitial.StatusLine(err), "skysocks", c.statusOverride); serr != nil && c.appCl != nil {
					c.appCl.Log().Debugf("route-down interstitial not served: %v", serr)
				}
				bc.Close() //nolint:errcheck,gosec
			}(conn)
			c.close()

			return fmt.Errorf("error opening yamux stream: %w", err)
		}

		if c.appCl != nil {
			c.appCl.Log().Debug("Opened session skysocks client")
		}

		go c.handleStream(conn, stream)
	}
}

// Liveness-probe tuning for sessionKeepAliveLoop. A route group can be
// torn down router-side (remote visor restart, all mux legs dropped)
// WITHOUT the underlying conn delivering EOF, so session.IsClosed() may
// never flip and ListenAndServe would block forever in Accept(). A timed
// yamux ping detects that. The interval/timeout are generous and we
// require consecutive failures so a merely-slow multihop route is not
// mistaken for a dead one (a false close just costs one reconnect cycle).
const (
	livenessProbeInterval = 15 * time.Second
	livenessProbeTimeout  = 10 * time.Second
	livenessFailThreshold = 2
)

func (c *Client) sessionKeepAliveLoop() {
	ticker := time.NewTicker(livenessProbeInterval)
	defer ticker.Stop()

	fails := 0
	for {
		select {
		case <-c.closeC:
			return
		case <-ticker.C:
			if c.session.IsClosed() {
				c.close()

				return
			}
			if c.sessionAlive(livenessProbeTimeout) {
				fails = 0

				continue
			}
			fails++
			if fails >= livenessFailThreshold {
				if c.appCl != nil {
					c.appCl.Log().Warnf("Liveness probe failed %dx; route group gone, closing for reconnect", fails)
				}
				c.close()

				return
			}
		}
	}
}

// sessionAlive reports whether a yamux ping round-trips within timeout.
// A silently torn-down rg conn never pongs; the timer catches that. The
// ping runs in a goroutine so a wedged ping cannot stall the loop —
// Close() tears down the session, which unblocks the pending ping.
func (c *Client) sessionAlive(timeout time.Duration) bool {
	done := make(chan error, 1)
	go func() {
		_, err := c.session.Ping()
		done <- err
	}()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(timeout):
		return false
	}
}

func (c *Client) handleStream(conn, stream net.Conn) {
	// Transparently sniff the SOCKS5 CONNECT target so a request for the reserved
	// status.skysocks host is answered in-process instead of tunneled to the
	// exit. For every non-status request the greeting/method negotiation and the
	// CONNECT request are forwarded to the exit byte-for-byte, so the exit sees an
	// identical stream — only status.skysocks diverges.
	if !c.sniffSOCKS5Status(conn, stream) {
		conn.Close()   //nolint:errcheck,gosec
		stream.Close() //nolint:errcheck,gosec
		if c.session.IsClosed() {
			c.close()
		}
		return
	}

	const errorCount = 2

	errCh := make(chan error, errorCount)

	go func() {
		_, err := io.Copy(stream, conn)
		errCh <- err
	}()

	go func() {
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()

	// Wait for both io.Copy goroutines to finish (exactly 2 sends).
	for i := 0; i < errorCount; i++ {
		if err := <-errCh; err != nil && c.appCl != nil {
			c.appCl.Log().Debugf("Copy error: %v", err)
		}
		// Close both sides after the first copy finishes to unblock the other.
		if i == 0 {
			conn.Close()   //nolint:errcheck,gosec
			stream.Close() //nolint:errcheck,gosec
		}
	}

	if c.session.IsClosed() {
		c.close()
	}
}

// statusSniffTimeout bounds the SOCKS5 handshake sniff so a wedged or
// non-SOCKS5 client can't pin a handleStream goroutine. The greeting and CONNECT
// request are sent right after connect, so this window is generous; it is
// cleared before the bidirectional data splice, which stays deadline-free.
const statusSniffTimeout = 15 * time.Second

// sniffSOCKS5Status inspects the SOCKS5 CONNECT target to intercept the reserved
// status.skysocks host. The greeting is forwarded to the exit verbatim and the
// exit's method-selection reply is forwarded to the browser verbatim; only the
// CONNECT request is parsed, and only a status.skysocks target diverges — every
// other target's request is written to the exit byte-for-byte before splicing,
// so non-status traffic is identical to a plain tunnel.
//
// Returns proceed=true when the caller should continue with the normal
// bidirectional splice (conn and stream are positioned just past the forwarded
// handshake). Returns false when the request was served in-process or the
// connection is unusable; the caller then closes both sides.
func (c *Client) sniffSOCKS5Status(conn, stream net.Conn) (proceed bool) {
	_ = conn.SetReadDeadline(time.Now().Add(statusSniffTimeout))   //nolint:errcheck
	_ = stream.SetReadDeadline(time.Now().Add(statusSniffTimeout)) //nolint:errcheck

	// Greeting: VER, NMETHODS, METHODS[NMETHODS] — forwarded to the exit verbatim.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 0x05 {
		return false
	}
	greeting := make([]byte, 2+int(hdr[1]))
	greeting[0], greeting[1] = hdr[0], hdr[1]
	if _, err := io.ReadFull(conn, greeting[2:]); err != nil {
		return false
	}
	if _, err := stream.Write(greeting); err != nil {
		return false
	}

	// Method-selection reply (2 bytes) from the exit — forwarded to the browser.
	method := make([]byte, 2)
	if _, err := io.ReadFull(stream, method); err != nil {
		return false
	}
	if _, err := conn.Write(method); err != nil {
		return false
	}
	// Anything but no-auth accepted: hand the rest off untouched — the auth
	// sub-negotiation and CONNECT ride through transparently.
	if method[0] != 0x05 || method[1] != 0x00 {
		clearDeadlines(conn, stream)
		return true
	}

	// CONNECT request: VER, CMD, RSV, ATYP, ADDR, PORT. Buffered so a non-status
	// target is forwarded to the exit byte-for-byte.
	rhdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, rhdr); err != nil || rhdr[0] != 0x05 {
		return false
	}
	req := append([]byte{}, rhdr...)
	var host string
	switch rhdr[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return false
		}
		host = net.IP(b).String()
		req = append(req, b...)
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return false
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return false
		}
		host = string(b)
		req = append(req, l[0])
		req = append(req, b...)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return false
		}
		host = net.IP(b).String()
		req = append(req, b...)
	default:
		// Unknown ATYP: forward what we have and let the exit deal with it.
		if _, err := stream.Write(req); err != nil {
			return false
		}
		clearDeadlines(conn, stream)
		return true
	}
	portB := make([]byte, 2)
	if _, err := io.ReadFull(conn, portB); err != nil {
		return false
	}
	req = append(req, portB...)

	// Reserved status host: serve the in-process page over HTTP instead of
	// tunneling to the exit. HTTP only — the resolver CA forbids a .skysocks TLS
	// leaf, so status.skysocks is HTTP-only by design.
	if surface, ok := proxystatus.Match(host); ok && surface == proxystatus.SurfaceSkysocks {
		clearDeadlines(conn, stream)
		c.serveStatusPage(conn, stream)
		return false
	}

	// Non-status: forward the buffered CONNECT request to the exit and splice.
	if _, err := stream.Write(req); err != nil {
		return false
	}
	clearDeadlines(conn, stream)
	return true
}

// statusOverride is the reserved-host answer handed to
// proxyinterstitial.ServeSOCKS5's route-down path: for the status.skysocks host
// it returns the rendered status page (the same one-shot bytes serveStatusPage
// writes), so the page stays reachable while the exit stream is down instead of
// being shadowed by the interstitial. Any other host returns nil, leaving the
// interstitial in place. statusSnapshot already reports "no active session to
// the exit" when the session is down, which is the correct content here. (Only
// the one-shot page is served on this path — the live WebSocket needs a duplex
// stream that the override's fixed-body model can't carry; the page's inline
// script reconnects the WebSocket once the conn is back.)
func (c *Client) statusOverride(host string) []byte {
	if surface, ok := proxystatus.Match(host); ok && surface == proxystatus.SurfaceSkysocks {
		return statusHTTPResponse(proxystatus.Render(c.statusSnapshot()))
	}
	return nil
}

// serveStatusPage completes the SOCKS5 CONNECT with a success reply, reads the
// browser's HTTP request, and routes on its path: "/ws" upgrades to a live,
// bidirectional WebSocket (serveStatusWS); anything else gets the one-shot
// rendered status.skysocks page. Best-effort: any write failure just drops the
// conn.
func (c *Client) serveStatusPage(conn, stream net.Conn) {
	// CONNECT success with a dummy BND.ADDR/PORT so the browser proceeds to send
	// its HTTP request.
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	// Read the request so we can route on its path. status.skysocks is HTTP-only
	// (the resolver CA forbids a .skysocks TLS leaf), so this is plaintext; a GET's
	// request line + headers arrive in the first packet, so one bounded read is
	// enough.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	buf := make([]byte, 2048)
	n, _ := conn.Read(buf)                //nolint:errcheck // best-effort; page is fixed
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	if statusRequestPath(buf[:n]) == "/ws" {
		// status is served entirely in-process, so the exit-side yamux stream is
		// unused — close it now so the long-lived WS loop doesn't pin one open.
		stream.Close() //nolint:errcheck,gosec
		if !wsHandshake(conn, buf[:n]) {
			return
		}
		c.serveStatusWS(conn)
		return
	}

	body := proxystatus.Render(c.statusSnapshot())
	_, _ = conn.Write(statusHTTPResponse(body)) //nolint:errcheck
}

// statusRequestPath extracts the request-target path from an HTTP request line
// ("METHOD SP PATH SP VERSION"), stripping any query string. It defaults to "/"
// for anything it can't parse, so a malformed request falls through to the
// full-page render rather than the WebSocket upgrade.
func statusRequestPath(req []byte) string {
	line := req
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := bytes.Fields(line)
	if len(fields) < 2 {
		return "/"
	}
	p := fields[1]
	if i := bytes.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return string(p)
}

// --- status.skysocks WebSocket control channel -------------------------------
//
// The live status region is pushed to the browser over a WebSocket (RFC6455)
// carried on the hijacked plaintext SOCKS stream, replacing the earlier one-way
// SSE stream. WebSocket is bidirectional: the same conn carries fragment pushes
// server→browser AND control commands browser→server, so the page can become a
// proxy control surface. The handshake and framing are hand-rolled (a few dozen
// lines) rather than pulling in a library, because the conn is an already-
// hijacked raw net.Conn with no http.ResponseWriter for a library's server
// Accept() to hijack.

// WebSocket loop tuning. wsPushInterval is how often a fresh live-region fragment
// is pushed — ~1s is responsive enough to watch a route warm without thrashing.
// wsPingInterval keeps an idle conn alive (the browser auto-replies PONG, which
// also resets the read deadline). wsReadTimeout must exceed wsPingInterval so an
// idle-but-healthy conn is not torn down; wsWriteTimeout bounds each write so a
// wedged browser errors out instead of blocking forever.
const (
	wsPushInterval = time.Second
	wsPingInterval = 25 * time.Second
	wsReadTimeout  = 90 * time.Second
	wsWriteTimeout = 10 * time.Second
)

// RFC6455 opcodes (only the ones this endpoint uses).
const (
	wsOpText  = 0x1
	wsOpClose = 0x8
	wsOpPing  = 0x9
	wsOpPong  = 0xA
)

// wsGUID is the RFC6455 magic appended to Sec-WebSocket-Key before the SHA-1.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsHandshake completes the RFC6455 opening handshake over the hijacked plaintext
// stream: it reads the client's Sec-WebSocket-Key out of the already-buffered
// request bytes and replies "101 Switching Protocols" with the computed
// Sec-WebSocket-Accept (base64(sha1(key + wsGUID))). Returns false (dropping the
// conn) when the request is not a well-formed upgrade.
func wsHandshake(conn net.Conn, req []byte) bool {
	key := httpHeaderValue(req, "Sec-WebSocket-Key")
	if key == "" || !strings.EqualFold(httpHeaderValue(req, "Upgrade"), "websocket") {
		return false
	}
	sum := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec // RFC6455 mandates SHA-1 here
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
	_, err := conn.Write([]byte(resp))
	_ = conn.SetWriteDeadline(time.Time{}) //nolint:errcheck
	return err == nil
}

// httpHeaderValue returns the value of the named header (case-insensitive) from a
// raw HTTP request, or "" if absent.
func httpHeaderValue(req []byte, name string) string {
	for _, ln := range strings.Split(string(req), "\n") {
		ln = strings.TrimRight(ln, "\r")
		i := strings.IndexByte(ln, ':')
		if i < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ln[:i]), name) {
			return strings.TrimSpace(ln[i+1:])
		}
	}
	return ""
}

// wsConn serializes server→client frame writes (the push loop, PONG replies and
// resync responses all share one conn) behind a mutex.
type wsConn struct {
	conn net.Conn
	mu   sync.Mutex
}

// write emits one server→client frame (never masked, per RFC6455) with a bounded
// write deadline.
func (w *wsConn) write(opcode byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
	return wsWriteFrame(w.conn, opcode, payload)
}

// statusControlCmd is the JSON shape of a browser→server control frame on the
// status.skysocks WebSocket, e.g. {"cmd":"resync"}. Kept deliberately small; the
// mux-control commands add fields here when they land (see handleStatusControl).
type statusControlCmd struct {
	Cmd string `json:"cmd"`
}

// serveStatusWS runs the bidirectional status WebSocket over the hijacked
// (already-upgraded) plaintext SOCKS stream. A reader goroutine consumes
// browser→server frames (control commands, PING, CLOSE) while the main loop
// pushes a fresh live-region fragment every wsPushInterval and a keepalive PING
// every wsPingInterval. It returns — releasing this goroutine, the reader and the
// conn — when the browser goes away (read/write error or CLOSE) or the client
// shuts down (closeC), so a session reconnect or proxy restart cannot leak it.
// The loop is ctx/timer-light (two tickers, no per-tick goroutine) so it is safe
// under the single-threaded wasm runtime, though that path is not normally
// exercised there.
func (c *Client) serveStatusWS(conn net.Conn) {
	w := &wsConn{conn: conn}
	stopC := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopC) }) }

	// Reader: browser→server frames. Blocks in wsReadFrame; the read deadline
	// (reset each iteration) plus closeC/stopC guarantee it can't wedge.
	go func() {
		defer stop()
		for {
			select {
			case <-stopC:
				return
			case <-c.closeC:
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout)) //nolint:errcheck
			op, payload, err := wsReadFrame(conn)
			if err != nil {
				return
			}
			switch op {
			case wsOpText:
				c.handleStatusControl(payload, w)
			case wsOpPing:
				if err := w.write(wsOpPong, payload); err != nil {
					return
				}
			case wsOpClose:
				return
			default:
				// wsOpPong / continuation / binary: ignore.
			}
		}
	}()

	// Push once immediately so the browser syncs without waiting a full tick.
	if err := w.write(wsOpText, proxystatus.RenderFragment(c.statusSnapshot())); err != nil {
		stop()
		return
	}
	push := time.NewTicker(wsPushInterval)
	ping := time.NewTicker(wsPingInterval)
	defer push.Stop()
	defer ping.Stop()
	for {
		select {
		case <-c.closeC:
			stop()
			return
		case <-stopC:
			return
		case <-push.C:
			if err := w.write(wsOpText, proxystatus.RenderFragment(c.statusSnapshot())); err != nil {
				stop()
				return
			}
		case <-ping.C:
			if err := w.write(wsOpPing, nil); err != nil {
				stop()
				return
			}
		}
	}
}

// handleStatusControl dispatches a browser→server control frame (JSON, e.g.
// {"cmd":"resync"}). This is the seam that turns the read-only status page into a
// proxy control surface. Only SAFE, already-available actions are wired here:
//
//   - "resync": push a fresh live-region fragment immediately.
//
// TODO(mux-control): the mux-op commands the page previews as disabled buttons —
// "add_leg", "drop_leg", "mux_mode", "rebuild" — are the next step. They mutate
// the surface's route group and so need an app→visor mux-control RPC that does not
// exist yet. The building blocks are already present (visor.RouteGroupMuxInfo for
// the current legs, router.AddMuxRoute to grow one, and the `cli proxy mux set`
// reconcile path for mode/rebuild); exposing them over the app RPC as a mutating
// proxystatus.Provider method is OUT OF SCOPE here. When that lands, add the cases
// below and enable the matching buttons in pkg/proxystatus/render.go.
func (c *Client) handleStatusControl(payload []byte, w *wsConn) {
	var cmd statusControlCmd
	if err := json.Unmarshal(payload, &cmd); err != nil {
		return
	}
	switch cmd.Cmd {
	case "resync":
		_ = w.write(wsOpText, proxystatus.RenderFragment(c.statusSnapshot())) //nolint:errcheck
	case "add_leg", "drop_leg", "mux_mode", "rebuild":
		// TODO(mux-control): wire once the app→visor mux-control RPC exists.
	default:
		// Unknown/unwired command: ignore.
	}
}

// wsWriteFrame writes a single unmasked (server→client) RFC6455 frame: FIN set,
// the given opcode, and payload. It implements the 7-bit, 16-bit (126) and 64-bit
// (127) length forms; the status fragment is a few KB, so the 16-bit path is the
// common one.
func wsWriteFrame(conn net.Conn, opcode byte, payload []byte) error {
	var hdr []byte
	b0 := byte(0x80) | opcode // FIN + opcode
	n := len(payload)
	switch {
	case n < 126:
		hdr = []byte{b0, byte(n)} //nolint:gosec // n<126 fits one byte
	case n < 1<<16:
		hdr = []byte{b0, 126, byte(n >> 8), byte(n)} //nolint:gosec // n<65536: high+low bytes
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n)) //nolint:gosec // len() is non-negative
		hdr = append([]byte{b0, 127}, ext[:]...)
	}
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	_, err := conn.Write(payload)
	return err
}

// wsReadFrame reads a single client→server RFC6455 frame and returns its opcode
// and unmasked payload. Client frames MUST be masked (RFC6455 §5.1); the 4-byte
// masking key is applied to the payload here. The 7-bit and 16-bit length forms
// are handled (a control command is tiny); a 64-bit length is rejected as
// oversized rather than trusted.
func wsReadFrame(conn net.Conn) (opcode byte, payload []byte, err error) {
	h := make([]byte, 2)
	if _, err = io.ReadFull(conn, h); err != nil {
		return 0, nil, err
	}
	opcode = h[0] & 0x0f
	masked := h[1]&0x80 != 0
	n := int(h[1] & 0x7f)
	switch n {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(conn, ext); err != nil {
			return 0, nil, err
		}
		n = int(ext[0])<<8 | int(ext[1])
	case 127:
		return 0, nil, fmt.Errorf("ws frame too large")
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err = io.ReadFull(conn, mask); err != nil {
			return 0, nil, err
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(conn, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return opcode, payload, nil
}

// statusSnapshot builds the read-only status.skysocks snapshot. The rich
// per-leg mux/log/event view is visor-side (see
// pkg/visor/embedded_proxystatus.go) because only the visor sees the route
// group; skysocks-client is a separate app process. It is fetched over the app
// RPC (ProxyStatus) and used as the base whenever the visor returned real data.
// The client's OWN truth about the live yamux session to the exit is then
// overlaid on top (Running + stream count). When the RPC is unavailable (the
// browser wasm-visor, or no route group yet) the base is the minimal local
// snapshot, so status.skysocks always renders.
func (c *Client) statusSnapshot() proxystatus.Snapshot {
	snap := c.visorStatusSnapshot()
	if c.session != nil && !c.session.IsClosed() {
		snap.Running = true
		snap.Note = fmt.Sprintf("session to the exit is up · %d open stream(s)", c.session.NumStreams())
	} else {
		snap.Note = "no active session to the exit"
	}
	return snap
}

// visorStatusSnapshot returns the visor-built rich snapshot when the app RPC is
// reachable and carried real data (any Legs/Logs/Events); otherwise the minimal
// local base. Kept separate so the RPC-unavailable path is a clean fallback that
// never breaks the status page.
func (c *Client) visorStatusSnapshot() proxystatus.Snapshot {
	base := proxystatus.Snapshot{
		Surface: proxystatus.SurfaceSkysocks,
		App:     skyenv.SkysocksClientName,
	}
	if c.appCl == nil {
		return base
	}
	rich, err := c.appCl.ProxyStatus()
	if err != nil {
		return base
	}
	if len(rich.Legs) == 0 && len(rich.Logs) == 0 && len(rich.Events) == 0 {
		return base
	}
	if rich.Surface == "" {
		rich.Surface = proxystatus.SurfaceSkysocks
	}
	if rich.App == "" {
		rich.App = skyenv.SkysocksClientName
	}
	return rich
}

// statusHTTPResponse wraps the page in a minimal close-delimited HTTP/1.1
// response (mirrors proxystatus.ServeConn's headers, which can't be reused here
// because we write onto the live browser conn rather than an in-memory pipe).
func statusHTTPResponse(body []byte) []byte {
	var b bytes.Buffer
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	b.WriteString("Cache-Control: no-store\r\n")
	b.WriteString("Connection: close\r\n\r\n")
	b.Write(body)
	return b.Bytes()
}

// clearDeadlines removes the sniff read deadlines before the deadline-free data
// splice takes over.
func clearDeadlines(conns ...net.Conn) {
	for _, cn := range conns {
		_ = cn.SetReadDeadline(time.Time{}) //nolint:errcheck
	}
}

func (c *Client) close() {
	if c.appCl != nil {
		c.appCl.Log().Debug("Session failed, closing skysocks client")
	}
	if err := c.Close(); err != nil && c.appCl != nil {
		c.appCl.Log().Errorf("Error closing skysocks client: %v", err)
	}
}

// ListenIPC starts named-pipe based connection server for windows or unix socket for other OSes
func (c *Client) ListenIPC(client *ipc.Client) {
	if c.appCl == nil {
		return
	}
	listenIPC(client, skyenv.SkysocksClientName, c.appCl.Log(), func() {
		client.Close()
		if err := c.Close(); err != nil {
			c.appCl.Log().Errorf("Error closing skysocks-client: %v", err)
		}
	})
}

func (c *Client) setAppError(appErr error) {
	if err := c.appCl.SetError(appErr.Error()); err != nil {
		c.appCl.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	var err error
	c.once.Do(func() {
		if c.appCl != nil {
			c.appCl.Log().Debug("Closing proxy client")
		}

		close(c.closeC)
		// Tear down the yamux session so reconnect builds a fresh one
		// and any in-flight liveness ping unblocks.
		if c.session != nil {
			err = c.session.Close()
		}
	})

	return err
}
