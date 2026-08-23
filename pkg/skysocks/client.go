// Package skysocks pkg/skysocks/client.go c4-app-proxy
package skysocks

import (
	"bytes"
	"fmt"
	"io"
	"net"
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
			// isn't delayed by a slow browser.
			go func(bc net.Conn) {
				if serr := proxyinterstitial.ServeSOCKS5(bc, proxyinterstitial.StatusLine(err), "skysocks"); serr != nil && c.appCl != nil {
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
		c.serveStatusPage(conn)
		return false
	}

	// Non-status: forward the buffered CONNECT request to the exit and splice.
	if _, err := stream.Write(req); err != nil {
		return false
	}
	clearDeadlines(conn, stream)
	return true
}

// serveStatusPage completes the SOCKS5 CONNECT with a success reply, drains the
// browser's (short, fixed-response) HTTP request, then writes the rendered
// status.skysocks page. Best-effort: any write failure just drops the conn.
func (c *Client) serveStatusPage(conn net.Conn) {
	// CONNECT success with a dummy BND.ADDR/PORT so the browser proceeds to send
	// its HTTP request.
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, _ = conn.Read(make([]byte, 2048))                      //nolint:errcheck // drain request; page is fixed
	_ = conn.SetReadDeadline(time.Time{})                     //nolint:errcheck
	body := proxystatus.Render(c.statusSnapshot())
	_, _ = conn.Write(statusHTTPResponse(body)) //nolint:errcheck
}

// statusSnapshot builds the read-only status.skysocks snapshot from the client's
// OWN state. The rich per-leg mux/log view is visor-side (see
// pkg/visor/embedded_proxystatus.go); skysocks-client is a separate app process
// whose app RPC exposes no status query, so rather than add cross-process
// plumbing the page reports what the client itself knows: session liveness and
// the number of open streams to the exit.
func (c *Client) statusSnapshot() proxystatus.Snapshot {
	snap := proxystatus.Snapshot{
		Surface: proxystatus.SurfaceSkysocks,
		App:     skyenv.SkysocksClientName,
	}
	if c.session != nil && !c.session.IsClosed() {
		snap.Running = true
		snap.Note = fmt.Sprintf("session to the exit is up · %d open stream(s)", c.session.NumStreams())
	} else {
		snap.Note = "no active session to the exit"
	}
	return snap
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
