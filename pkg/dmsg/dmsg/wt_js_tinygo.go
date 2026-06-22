//go:build tinygo && js && wasm

// Package dmsg pkg/dmsg/dmsg/wt_js_tinygo.go: dmsg-over-WebTransport CLIENT dial
// for the TinyGo browser target.
//
// wt.go (the native WebTransport carrier) pulls quic-go + webtransport-go, which
// don't compile under TinyGo. Here we dial the browser's native WebTransport API
// directly via syscall/js and adapt one bidirectional stream to a net.Conn —
// flowing through the SAME makeClientSession + yamux path as the WS/TCP carriers.
//
// WebTransport is the one browser-reachable carrier that needs NO CA: the
// WebTransport constructor accepts a self-signed server cert pinned by SHA-256
// via serverCertificateHashes. We pin Server.CertHashWT exactly as the native
// path pins it in VerifyPeerCertificate — so a browser reaches a dmsg server by
// bare IP, no domain, no Let's Encrypt. The client→server skywire PK is still
// authenticated in the dmsg Noise handshake, identical to TCP/WS.
package dmsg

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall/js"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// dialSessionWT dials the dmsg server's WebTransport endpoint (Server.AddressWT)
// via the browser WebTransport API and builds a yamux+Noise client session over
// one bidirectional stream — the TinyGo-browser analog of wt.go's dialSessionWT.
func (ce *Client) dialSessionWT(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	conn, err := dialWebTransportJS(ctx, entry.Server.AddressWT, entry.Server.CertHashWT)
	if err != nil {
		return ClientSession{}, fmt.Errorf("wt(js): dial %q: %w", entry.Server.AddressWT, err)
	}

	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	dSes.sm.yamux, err = yamux.Client(conn, YamuxConfig())
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	ce.log.Infof("wt stream session initial for %s", dSes.RemotePK().String())
	return dSes, nil
}

// awaitJS blocks until the JS promise p settles, returning its resolved value or
// an error built from the rejection reason. It parks the goroutine (off the JS
// event loop) until a then/catch callback fires.
func awaitJS(p js.Value) (js.Value, error) {
	ch := make(chan struct{})
	var res js.Value
	var rejErr error
	onOK := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		if len(args) > 0 {
			res = args[0]
		}
		close(ch)
		return nil
	})
	onErr := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		msg := "promise rejected"
		if len(args) > 0 {
			if m := args[0].Get("message"); m.Type() == js.TypeString {
				msg = m.String()
			} else {
				msg = args[0].Call("toString").String()
			}
		}
		rejErr = errors.New(msg)
		close(ch)
		return nil
	})
	p.Call("then", onOK).Call("catch", onErr)
	<-ch
	onOK.Release()
	onErr.Release()
	return res, rejErr
}

// dialWebTransportJS opens a browser WebTransport to url (pinning certHashHex),
// awaits ready, opens one bidirectional stream, and returns it as a net.Conn.
func dialWebTransportJS(ctx context.Context, url, certHashHex string) (*wtConnJS, error) {
	ctor := js.Global().Get("WebTransport")
	if !ctor.Truthy() {
		return nil, errors.New("no WebTransport constructor in this JS environment")
	}
	wantHash, err := hex.DecodeString(certHashHex)
	if err != nil || len(wantHash) != 32 {
		return nil, fmt.Errorf("invalid cert hash %q", certHashHex)
	}

	// serverCertificateHashes: [{ algorithm: "sha-256", value: Uint8Array }]
	hashU8 := js.Global().Get("Uint8Array").New(len(wantHash))
	js.CopyBytesToJS(hashU8, wantHash)
	hashEntry := js.Global().Get("Object").New()
	hashEntry.Set("algorithm", "sha-256")
	hashEntry.Set("value", hashU8)
	hashes := js.Global().Get("Array").New()
	hashes.Call("push", hashEntry)
	opts := js.Global().Get("Object").New()
	opts.Set("serverCertificateHashes", hashes)

	wt := ctor.New(url, opts)

	// Honour ctx during the ready/open handshakes by racing a watcher that
	// closes the transport on cancellation (which rejects the pending promises).
	watchDone := make(chan struct{})
	defer close(watchDone)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				wt.Call("close")
			case <-watchDone:
			}
		}()
	}

	if _, err := awaitJS(wt.Get("ready")); err != nil {
		return nil, fmt.Errorf("ready: %w", err)
	}
	streamV, err := awaitJS(wt.Call("createBidirectionalStream"))
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	if err := ctx.Err(); err != nil {
		wt.Call("close")
		return nil, err
	}

	c := &wtConnJS{
		wt:     wt,
		reader: streamV.Get("readable").Call("getReader"),
		writer: streamV.Get("writable").Call("getWriter"),
		addr:   wsAddr(url),
		notify: make(chan struct{}),
	}
	go c.readPump()
	return c, nil
}

// wtConnJS adapts a WebTransport bidirectional stream (WHATWG readable/writable)
// to a net.Conn. A pump goroutine drains the ReadableStream into a buffer; Read
// serves from the buffer (deadline-aware); Write awaits writer.write so yamux
// sees real backpressure. It reuses wsAddr / wsTimeoutError from ws_js_tinygo.go.
type wtConnJS struct {
	wt     js.Value
	reader js.Value
	writer js.Value
	addr   wsAddr

	mu        sync.Mutex
	buf       []byte
	notify    chan struct{}
	closed    bool
	closeErr  error
	rDeadline time.Time
}

// readPump loops reader.read() and appends inbound bytes until done/error/close.
func (c *wtConnJS) readPump() {
	for {
		v, err := awaitJS(c.reader.Call("read"))
		if err != nil {
			c.fail(err)
			return
		}
		if v.Get("done").Bool() {
			c.fail(io.EOF)
			return
		}
		val := v.Get("value") // Uint8Array
		b := make([]byte, val.Get("length").Int())
		js.CopyBytesToGo(b, val)
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.buf = append(c.buf, b...)
		c.wake()
		c.mu.Unlock()
	}
}

// wake broadcasts to blocked readers. Caller must hold c.mu.
func (c *wtConnJS) wake() {
	close(c.notify)
	c.notify = make(chan struct{})
}

func (c *wtConnJS) fail(err error) {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.closeErr = err
		c.wake()
	}
	c.mu.Unlock()
}

func (c *wtConnJS) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.buf) > 0 {
			n := copy(p, c.buf)
			c.buf = c.buf[n:]
			c.mu.Unlock()
			return n, nil
		}
		if c.closed {
			err := c.closeErr
			c.mu.Unlock()
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		notify := c.notify
		deadline := c.rDeadline
		c.mu.Unlock()

		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, wsTimeoutError{}
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-notify:
		case <-timeout:
			return 0, wsTimeoutError{}
		}
	}
}

func (c *wtConnJS) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = net.ErrClosed
		}
		return 0, err
	}
	c.mu.Unlock()

	u8 := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(u8, p)
	if _, err := awaitJS(c.writer.Call("write", u8)); err != nil {
		c.fail(err)
		return 0, err
	}
	return len(p), nil
}

func (c *wtConnJS) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.closeErr == nil {
		c.closeErr = net.ErrClosed
	}
	c.wake()
	c.mu.Unlock()
	c.wt.Call("close")
	return nil
}

func (c *wtConnJS) LocalAddr() net.Addr  { return c.addr }
func (c *wtConnJS) RemoteAddr() net.Addr { return c.addr }

func (c *wtConnJS) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.wake()
	c.mu.Unlock()
	return nil
}
func (c *wtConnJS) SetWriteDeadline(time.Time) error { return nil }
func (c *wtConnJS) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }
