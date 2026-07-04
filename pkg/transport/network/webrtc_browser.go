//go:build js && wasm

// Package network pkg/transport/network/webrtc_browser.go
//
// Browser WebRTC carrier: dial/accept a direct DataChannel via the browser-native
// RTCPeerConnection (syscall/js), signaling over the same dmsg-backed signalConn
// the native pion carrier uses — so a browser visor and a native pion visor
// interoperate (identical wire format). This ports the proven cmd/dmsg-wasm
// webrtc_js.go logic to the network.Client interface.
//
// Used for ALL js/wasm builds (standard Go and TinyGo): pion's js backend looks up
// `window.RTCPeerConnection`, which is absent in a Web Worker (no `window`), so the
// wasm-visor — whose Go runtime runs in a worker — can't use pion. This hand-rolled
// carrier instead goes through newPeerConnection, which prefers a main-thread
// RTCPeerConnection PROXY (globalThis.__skywireRTC, installed by pkg/wasmhv
// worker.js and driven by hv-boot.js) when present, and falls back to a direct
// RTCPeerConnection on the page main thread otherwise.
//
// Start + the dmsg signaling listener are shared (untagged, webrtc.go); this file
// supplies the build-tagged webrtcDial (offerer) + webrtcAccept (answerer).
package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall/js"
	"time"
)

// awaitJS blocks until the JS promise p settles, returning its resolved value or
// an error built from the rejection reason.
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

// iceServersJS builds the RTCPeerConnection iceServers array from URLs (or a
// falsy value for none).
func iceServersJS(iceURLs []string) js.Value {
	if len(iceURLs) == 0 {
		return js.Undefined()
	}
	arr := js.Global().Get("Array").New()
	for _, u := range iceURLs {
		s := js.Global().Get("Object").New()
		s.Set("urls", u)
		arr.Call("push", s)
	}
	return arr
}

// newPeerConnection constructs an RTCPeerConnection with the given iceServers.
// Prefers the main-thread proxy (globalThis.__skywireRTC.newPC) when present — the
// wasm-visor's Go runtime runs in a Web Worker with no RTCPeerConnection, so the
// proxy forwards to a real one on the page main thread. Falls back to a direct
// RTCPeerConnection when on the main thread (the in-page boot fallback) or under a
// harness that exposes RTCPeerConnection to the worker.
func newPeerConnection(iceServers js.Value) js.Value {
	if b := js.Global().Get("__skywireRTC"); b.Truthy() {
		return b.Call("newPC", iceServers)
	}
	cfg := js.Global().Get("Object").New()
	if iceServers.Truthy() {
		cfg.Set("iceServers", iceServers)
	}
	return js.Global().Get("RTCPeerConnection").New(cfg)
}

// wireLocalCandidates forwards locally-gathered ICE candidates to the peer over
// the signaling channel (trickle ICE).
func wireLocalCandidates(pc js.Value, sc *signalConn) {
	onCand := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		cand := args[0].Get("candidate")
		if !cand.Truthy() {
			return nil // null candidate = gathering complete
		}
		mid := ""
		if v := cand.Get("sdpMid"); v.Truthy() {
			mid = v.String()
		}
		line := 0
		if v := cand.Get("sdpMLineIndex"); v.Truthy() {
			line = v.Int()
		}
		_ = sc.send(signalMsg{Type: "candidate", Candidate: cand.Get("candidate").String(), SDPMid: mid, SDPMLine: line}) //nolint:errcheck
		return nil
	})
	pc.Set("onicecandidate", onCand)
}

// pumpRemoteSignals reads signaling messages from sc and applies them to pc.
// onOffer (answerer only) handles an inbound offer; for the offerer it is nil and
// an "answer" sets the remote description. Candidates arriving before the remote
// description is set are buffered (the trickle-ICE ordering fix).
func pumpRemoteSignals(ctx context.Context, pc js.Value, sc *signalConn, onOffer func(sdp string) error) {
	remoteSet := false
	var queued []signalMsg
	addCand := func(m signalMsg) {
		cand := js.Global().Get("Object").New()
		cand.Set("candidate", m.Candidate)
		cand.Set("sdpMid", m.SDPMid)
		cand.Set("sdpMLineIndex", m.SDPMLine)
		_, _ = awaitJS(pc.Call("addIceCandidate", cand)) //nolint:errcheck
	}
	flush := func() {
		remoteSet = true
		for _, c := range queued {
			addCand(c)
		}
		queued = nil
	}
	for {
		if ctx.Err() != nil {
			return
		}
		m, err := sc.recv()
		if err != nil {
			return
		}
		switch m.Type {
		case "offer":
			if onOffer != nil {
				if err := onOffer(m.SDP); err == nil {
					flush()
				}
			}
		case "answer":
			desc := map[string]interface{}{"type": "answer", "sdp": m.SDP}
			if _, err := awaitJS(pc.Call("setRemoteDescription", desc)); err == nil {
				flush()
			}
		case "candidate":
			if remoteSet {
				addCand(m)
			} else {
				queued = append(queued, m)
			}
		}
	}
}

// webrtcDial is the OFFERER: create the peer connection + DataChannel, send the
// offer over the signaling stream, apply the answer + remote candidates, and
// return once the DataChannel is open.
func webrtcDial(ctx context.Context, signal io.ReadWriteCloser, iceURLs []string) (net.Conn, error) {
	sc := &signalConn{rwc: signal}
	pc := newPeerConnection(iceServersJS(iceURLs))
	dc := pc.Call("createDataChannel", "skywire", map[string]interface{}{"ordered": true})
	conn := newWebRTCConn(dc, pc, signal)

	wireLocalCandidates(pc, sc)
	go pumpRemoteSignals(ctx, pc, sc, nil)

	offer, err := awaitJS(pc.Call("createOffer"))
	if err != nil {
		return nil, fmt.Errorf("webrtc: createOffer: %w", err)
	}
	if _, err := awaitJS(pc.Call("setLocalDescription", offer)); err != nil {
		return nil, fmt.Errorf("webrtc: setLocalDescription: %w", err)
	}
	if err := sc.send(signalMsg{Type: "offer", SDP: offer.Get("sdp").String()}); err != nil {
		return nil, fmt.Errorf("webrtc: send offer: %w", err)
	}
	if err := conn.waitOpen(ctx); err != nil {
		return nil, err
	}
	return conn, nil
}

// webrtcAccept is the ANSWERER: wait for the offer on the signaling stream, answer
// it, and return the DataChannel (delivered via pc.ondatachannel) once open.
func webrtcAccept(ctx context.Context, signal io.ReadWriteCloser, iceURLs []string) (net.Conn, error) {
	sc := &signalConn{rwc: signal}
	pc := newPeerConnection(iceServersJS(iceURLs))
	dcCh := make(chan js.Value, 1)
	onDC := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		select {
		case dcCh <- args[0].Get("channel"):
		default:
		}
		return nil
	})
	pc.Set("ondatachannel", onDC)

	wireLocalCandidates(pc, sc)
	go pumpRemoteSignals(ctx, pc, sc, func(offerSDP string) error {
		desc := map[string]interface{}{"type": "offer", "sdp": offerSDP}
		if _, err := awaitJS(pc.Call("setRemoteDescription", desc)); err != nil {
			return fmt.Errorf("setRemoteDescription(offer): %w", err)
		}
		answer, err := awaitJS(pc.Call("createAnswer"))
		if err != nil {
			return fmt.Errorf("createAnswer: %w", err)
		}
		if _, err := awaitJS(pc.Call("setLocalDescription", answer)); err != nil {
			return fmt.Errorf("setLocalDescription(answer): %w", err)
		}
		return sc.send(signalMsg{Type: "answer", SDP: answer.Get("sdp").String()})
	})

	select {
	case dc := <-dcCh:
		conn := newWebRTCConn(dc, pc, signal)
		if err := conn.waitOpen(ctx); err != nil {
			return nil, err
		}
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// webRTCConn adapts an RTCDataChannel to net.Conn (binaryType=arraybuffer), using
// the same buffer+notify+deadline pattern as the WebSocket/WebTransport browser
// conns. Close tears down the DataChannel, peer connection, and signaling stream.
type webRTCConn struct {
	dc     js.Value
	pc     js.Value
	signal io.Closer

	mu        sync.Mutex
	buf       []byte
	notify    chan struct{}
	openCh    chan struct{}
	openOnce  sync.Once
	openErr   error
	closed    bool
	closeErr  error
	rDeadline time.Time

	handlers []js.Func
}

func newWebRTCConn(dc, pc js.Value, signal io.Closer) *webRTCConn {
	dc.Set("binaryType", "arraybuffer")
	c := &webRTCConn{dc: dc, pc: pc, signal: signal, notify: make(chan struct{}), openCh: make(chan struct{})}

	onOpen := js.FuncOf(func(js.Value, []js.Value) interface{} {
		c.openOnce.Do(func() { close(c.openCh) })
		return nil
	})
	onMsg := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		u8 := js.Global().Get("Uint8Array").New(args[0].Get("data"))
		b := make([]byte, u8.Length())
		js.CopyBytesToGo(b, u8)
		c.mu.Lock()
		c.buf = append(c.buf, b...)
		c.wake()
		c.mu.Unlock()
		return nil
	})
	onClose := js.FuncOf(func(js.Value, []js.Value) interface{} {
		c.fail(io.EOF)
		c.openOnce.Do(func() { c.openErr = errors.New("datachannel closed before open"); close(c.openCh) })
		return nil
	})
	onErr := js.FuncOf(func(js.Value, []js.Value) interface{} {
		c.fail(errors.New("datachannel error"))
		c.openOnce.Do(func() { c.openErr = errors.New("datachannel error before open"); close(c.openCh) })
		return nil
	})
	c.handlers = []js.Func{onOpen, onMsg, onClose, onErr}
	dc.Set("onopen", onOpen)
	dc.Set("onmessage", onMsg)
	dc.Set("onclose", onClose)
	dc.Set("onerror", onErr)

	if dc.Get("readyState").String() == "open" {
		c.openOnce.Do(func() { close(c.openCh) })
	}
	return c
}

func (c *webRTCConn) waitOpen(ctx context.Context) error {
	select {
	case <-c.openCh:
		return c.openErr
	case <-ctx.Done():
		c.Close() //nolint:errcheck
		return ctx.Err()
	}
}

func (c *webRTCConn) wake() { close(c.notify); c.notify = make(chan struct{}) }

func (c *webRTCConn) fail(err error) {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.closeErr = err
		c.wake()
	}
	c.mu.Unlock()
}

func (c *webRTCConn) Read(p []byte) (int, error) {
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
				return 0, wrtcTimeout{}
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-notify:
		case <-timeout:
			return 0, wrtcTimeout{}
		}
	}
}

func (c *webRTCConn) Write(p []byte) (int, error) {
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
	c.dc.Call("send", u8)
	return len(p), nil
}

func (c *webRTCConn) Close() error {
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
	c.dc.Call("close")
	c.pc.Call("close")
	if c.signal != nil {
		c.signal.Close() //nolint:errcheck
	}
	return nil
}

func (c *webRTCConn) LocalAddr() net.Addr  { return webrtcAddr{} }
func (c *webRTCConn) RemoteAddr() net.Addr { return webrtcAddr{} }

func (c *webRTCConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.wake()
	c.mu.Unlock()
	return nil
}
func (c *webRTCConn) SetWriteDeadline(time.Time) error { return nil }
func (c *webRTCConn) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }

type wrtcTimeout struct{}

func (wrtcTimeout) Error() string   { return "webrtc: i/o timeout" }
func (wrtcTimeout) Timeout() bool   { return true }
func (wrtcTimeout) Temporary() bool { return true }
