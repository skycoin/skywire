// Package visor pkg/visor/api_browse_ws.go c3-vis-browse
package visor

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/net/websocket"
)

// A page in a real-origin browse frame opens its WebSockets through here:
// the frame's WebSocket is a stand-in that hands the connection to the desk,
// which asks this relay to open the real one through the browse proxy. A
// browser's own WebSocket goes straight to the network from the reader's
// address — a service worker never sees it.
//
// golang.org/x/net/websocket rather than the vendored coder/websocket: under
// js/wasm coder/websocket is the BROWSER's WebSocket, the very leak this
// closes. x/net's is pure Go over whatever net.Conn it is handed.
//
// Between the relay and the desk, each message is a frame:
// [type u8][length u32 BE][payload].
const (
	browseWSText   = 1
	browseWSBinary = 2
	browseWSClose  = 8  // payload: code u16 BE + reason
	browseWSOpen   = 9  // payload: the negotiated subprotocol
	browseWSError  = 10 // payload: message

	// BrowseWSUpgrade names the upgrade the desk asks for.
	BrowseWSUpgrade = "skywire-browse-ws"

	browseWSMaxMessage   = 16 << 20
	browseWSDialTimeout  = 30 * time.Second
	browseWSWriteTimeout = 30 * time.Second
)

// BrowseWebSocketRequest is one WebSocket a browse frame wants opened.
type BrowseWebSocketRequest struct {
	URL       string   // ws:// or wss://
	Proxy     string   // the browse proxy, as for BrowseClearnetRequest
	Origin    string   // the Origin header a browser on the page's real site sends
	Protocols []string // requested subprotocols
}

// BrowseWebSocket opens req.URL through req.Proxy, sending the jar's cookies
// for that site as a browser would.
func (v *Visor) BrowseWebSocket(ctx context.Context, req BrowseWebSocketRequest) (*websocket.Conn, error) {
	target, err := url.Parse(req.URL)
	if err != nil || (target.Scheme != "ws" && target.Scheme != "wss") || target.Host == "" {
		return nil, fmt.Errorf("not a ws:// or wss:// url: %q", req.URL)
	}
	pu, err := parseBrowseProxy(req.Proxy)
	if err != nil {
		return nil, err
	}
	host := target.Hostname()
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "wss" {
			port = "443"
		}
	}
	addr := net.JoinHostPort(host, port)

	var raw net.Conn
	switch pu.Scheme {
	case "socks5", "socks5h":
		sd, err := proxy.SOCKS5("tcp", pu.Host, nil, browseProxyDialer{})
		if err != nil {
			return nil, err
		}
		raw, err = sd.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s via proxy %s: %w", addr, pu.Redacted(), err)
		}
	default:
		return nil, fmt.Errorf("websocket relay needs a socks5 proxy, not %s", pu.Scheme)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl) //nolint:errcheck
	}
	conn := raw
	if target.Scheme == "wss" {
		tc := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: browseRootCAs(), MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = raw.Close() //nolint:errcheck
			return nil, fmt.Errorf("tls to %s: %w", host, err)
		}
		conn = tc
	}

	origin := req.Origin
	if origin == "" {
		origin = "https://" + host
	}
	cfg, err := websocket.NewConfig(target.String(), origin)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	cfg.Protocol = req.Protocols
	if jar := browseJar(); jar != nil {
		cookieURL := *target
		cookieURL.Scheme = strings.Replace(target.Scheme, "ws", "http", 1)
		var parts []string
		for _, c := range jar.Cookies(&cookieURL) {
			parts = append(parts, c.Name+"="+c.Value)
		}
		if len(parts) > 0 {
			cfg.Header = http.Header{"Cookie": {strings.Join(parts, "; ")}}
		}
	}
	ws, err := websocket.NewClient(cfg, conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("websocket handshake with %s: %w", target.Host, err)
	}
	_ = raw.SetDeadline(time.Time{}) //nolint:errcheck
	ws.MaxPayloadBytes = browseWSMaxMessage
	return ws, nil
}

// browseWSMessage carries one received message and its kind.
type browseWSMessage struct {
	data   []byte
	binary bool
}

// browseWSCodec reads a message keeping whether it was text or binary, which
// websocket.Message folds away.
var browseWSCodec = websocket.Codec{
	Marshal: func(v interface{}) ([]byte, byte, error) {
		m := v.(browseWSMessage)
		if m.binary {
			return m.data, websocket.BinaryFrame, nil
		}
		return m.data, websocket.TextFrame, nil
	},
	Unmarshal: func(data []byte, payloadType byte, v interface{}) error {
		m := v.(*browseWSMessage)
		m.data = data
		m.binary = payloadType == websocket.BinaryFrame
		return nil
	},
}

// relayBrowseWS pumps messages between the desk's conn and the site's
// WebSocket until either side ends, then closes both.
func relayBrowseWS(desk net.Conn, ws *websocket.Conn, protocol string) {
	var once sync.Once
	var wmu sync.Mutex
	done := func() {
		once.Do(func() {
			_ = ws.Close()   //nolint:errcheck
			_ = desk.Close() //nolint:errcheck
		})
	}
	writeDesk := func(t byte, payload []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = desk.SetWriteDeadline(time.Now().Add(browseWSWriteTimeout)) //nolint:errcheck
		return writeBrowseWSFrame(desk, t, payload)
	}
	if err := writeDesk(browseWSOpen, []byte(protocol)); err != nil {
		done()
		return
	}

	go func() {
		defer done()
		for {
			var m browseWSMessage
			if err := browseWSCodec.Receive(ws, &m); err != nil {
				code := make([]byte, 2)
				binary.BigEndian.PutUint16(code, 1006) // abnormal: x/net does not surface the peer's code
				if errors.Is(err, io.EOF) {
					binary.BigEndian.PutUint16(code, 1000)
				}
				_ = writeDesk(browseWSClose, code) //nolint:errcheck
				return
			}
			t := byte(browseWSText)
			if m.binary {
				t = browseWSBinary
			}
			if writeDesk(t, m.data) != nil {
				return
			}
		}
	}()

	defer done()
	for {
		t, payload, err := readBrowseWSFrame(desk)
		if err != nil {
			return
		}
		switch t {
		case browseWSText, browseWSBinary:
			_ = ws.SetWriteDeadline(time.Now().Add(browseWSWriteTimeout)) //nolint:errcheck
			if browseWSCodec.Send(ws, browseWSMessage{data: payload, binary: t == browseWSBinary}) != nil {
				return
			}
		case browseWSClose:
			return
		}
	}
}

func writeBrowseWSFrame(w io.Writer, t byte, payload []byte) error {
	hdr := make([]byte, 5)
	hdr[0] = t
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload))) //nolint:gosec // bounded by browseWSMaxMessage
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readBrowseWSFrame(r io.Reader) (byte, []byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > browseWSMaxMessage {
		return 0, nil, fmt.Errorf("browse ws frame of %d bytes exceeds %d", n, browseWSMaxMessage)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

// serveBrowseWS answers the desk's upgrade: it opens the site's WebSocket
// first, so a failure is an ordinary HTTP error the desk can report, then
// takes over the connection and relays.
func (v *Visor) serveBrowseWS(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), BrowseWSUpgrade) {
		http.Error(w, "expected Upgrade: "+BrowseWSUpgrade, http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	req := BrowseWebSocketRequest{URL: q.Get("url"), Proxy: q.Get("proxy"), Origin: q.Get("origin")}
	if p := strings.TrimSpace(q.Get("protocols")); p != "" {
		for _, s := range strings.Split(p, ",") {
			if s = strings.TrimSpace(s); s != "" {
				req.Protocols = append(req.Protocols, s)
			}
		}
	}
	// Not r.Context(): the relay outlives the request.
	ctx, cancel := context.WithTimeout(context.Background(), browseWSDialTimeout)
	ws, err := v.BrowseWebSocket(ctx, req)
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = ws.Close() //nolint:errcheck
		http.Error(w, "connection cannot be taken over", http.StatusInternalServerError)
		return
	}
	desk, rw, err := hj.Hijack()
	if err != nil {
		_ = ws.Close() //nolint:errcheck
		return
	}
	if rw.Reader.Buffered() > 0 {
		desk = &browseWSConn{Conn: desk, r: rw.Reader}
	}
	_ = desk.SetDeadline(time.Time{}) //nolint:errcheck
	if _, err := io.WriteString(desk, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: "+BrowseWSUpgrade+"\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		_ = ws.Close()   //nolint:errcheck
		_ = desk.Close() //nolint:errcheck
		return
	}
	protocol := ""
	if len(ws.Config().Protocol) > 0 {
		protocol = ws.Config().Protocol[0]
	}
	go relayBrowseWS(desk, ws, protocol)
}

// browseWSConn reads first what the HTTP server had already buffered.
type browseWSConn struct {
	net.Conn
	r io.Reader
}

func (c *browseWSConn) Read(p []byte) (int, error) { return c.r.Read(p) }
