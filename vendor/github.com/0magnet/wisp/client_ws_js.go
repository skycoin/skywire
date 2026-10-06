//go:build js

// Dialing a Wisp endpoint by URL from inside a browser.
//
// coder/websocket's js/wasm build wraps the page's own WebSocket object, whose
// DialOptions carry a subprotocol list and nothing else. That is not a gap in
// the library: the WebSocket API gives a page no way to set request headers
// (the subprotocol is the only one it can influence) and no way to supply a
// transport of its own.
//
// So the two options that cannot be honored are refused rather than ignored.
// Silently dropping a proxy the caller asked for would send the traffic
// straight out of the page instead — the precise thing the caller was trying
// to avoid. The refusal names DialConn, which is how that is actually done in
// a tab: dial the conn yourself, over whatever you like, and run the session
// on it.

package wisp

import (
	"context"
	"errors"
	"fmt"

	"github.com/coder/websocket"
)

// dialWebsocket opens the endpoint and wraps it as a transport.
func dialWebsocket(ctx context.Context, cfg ClientConfig) (Frames, error) {
	if cfg.HTTPClient != nil {
		return nil, errors.New("wisp: ClientConfig.HTTPClient cannot be honored in a browser — " +
			"a page's WebSocket has no transport to replace; dial the conn yourself and use DialConn")
	}
	if len(cfg.Header) > 0 {
		return nil, errors.New("wisp: ClientConfig.Header cannot be honored in a browser — " +
			"a page cannot set WebSocket request headers; dial the conn yourself and use DialConn")
	}

	// coder/websocket closes with 1008 when the dial's context ends first,
	// so the dial gets none and the deadline is kept here (see browserFrames).
	type dialed struct {
		conn *websocket.Conn
		err  error
	}
	ch := make(chan dialed, 1)
	go func() { //nolint:gosec // G118: a context here is what makes it close with 1008
		conn, _, err := websocket.Dial(context.Background(), cfg.URL, &websocket.DialOptions{ //nolint:bodyclose // no body on js
			Subprotocols: []string{Subprotocol},
		})
		ch <- dialed{conn, err}
	}()
	select {
	case d := <-ch:
		if d.err != nil {
			return nil, fmt.Errorf("wisp: dial %s: %w", cfg.URL, d.err)
		}
		d.conn.SetReadLimit(-1)
		return &browserFrames{conn: d.conn, limit: cfg.ReadLimit}, nil
	case <-ctx.Done():
		go func() {
			if d := <-ch; d.err == nil {
				closeWebsocket(d.conn) //nolint:errcheck,gosec // nobody is waiting for it
			}
		}()
		return nil, fmt.Errorf("wisp: dial %s: %w", cfg.URL, ctx.Err())
	}
}

// closeWebsocket closes a page's WebSocket with 1000, normal closure.
// CloseNow would send 1001, and WebSocket.close throws on any code but 1000
// and 3000–4999: under TinyGo that throw is a panic that kills the program.
// Close waits for the close handshake, so it runs on its own goroutine rather
// than hold up the session's teardown.
func closeWebsocket(conn *websocket.Conn) error {
	go conn.Close(websocket.StatusNormalClosure, "") //nolint:errcheck // best effort on the way out
	return nil
}

// browserFrames is wsFrames for a page's WebSocket. coder/websocket's js
// build closes with a code the browser refuses whenever a read's context
// ends (1008) or a message is over the read limit (1009), so it is given
// neither: the context and the limit are kept here, and the conn is closed
// with 1000 when either is hit.
type browserFrames struct {
	conn  *websocket.Conn
	limit int64
}

// ReadFrame implements Frames, skipping text frames as wsFrames does.
func (f *browserFrames) ReadFrame(ctx context.Context) ([]byte, error) {
	type read struct {
		data []byte
		err  error
	}
	ch := make(chan read, 1)
	go func() { //nolint:gosec // G118: a context here is what makes it close with 1008
		for {
			typ, data, err := f.conn.Read(context.Background())
			if err == nil && typ != websocket.MessageBinary {
				continue
			}
			ch <- read{data, err}
			return
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if f.limit >= 0 && int64(len(r.data)) > f.limit {
			f.Close() //nolint:errcheck,gosec // the limit is the error to report
			return nil, fmt.Errorf("wisp: %d-byte frame is over the read limit of %d", len(r.data), f.limit)
		}
		return r.data, nil
	case <-ctx.Done():
		// Ending the read means ending the conn, as coder/websocket does:
		// the reader above would otherwise take the next frame.
		f.Close() //nolint:errcheck,gosec // the context is the error to report
		return nil, ctx.Err()
	}
}

// WriteFrame implements Frames. A browser's send never blocks, so the
// context has nothing to bound.
func (f *browserFrames) WriteFrame(ctx context.Context, b []byte) error {
	return f.conn.Write(ctx, websocket.MessageBinary, b)
}

// Close implements Frames.
func (f *browserFrames) Close() error { return closeWebsocket(f.conn) }
