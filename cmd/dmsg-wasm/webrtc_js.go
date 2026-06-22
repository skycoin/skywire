//go:build js && wasm

// Package main — WebRTC DataChannel p2p transport for the browser dmsg client.
//
// This is the genuinely peer-to-peer transport: WebRTC's DTLS+SCTP DataChannel
// is a direct, encrypted pipe between two browsers (or a browser and a native
// pion peer), with NAT traversal via ICE. Unlike WebSocket/WebTransport (which
// reach a dmsg SERVER), a DataChannel connects two LEAVES directly — no relay
// carries the payload.
//
// WebRTC needs a signaling side-channel to exchange the SDP offer/answer and ICE
// candidates BEFORE the direct pipe exists. dmsg is that side-channel: the two
// peers already share dmsg connectivity, so the offer/answer/candidates ride a
// dmsg stream (SignalChannel). Once the DataChannel opens, it is adapted to a
// net.Conn — ready to carry a Noise+yamux session exactly like every other
// carrier, so the SAME upper stack runs over a browser-native p2p link.
//
// Status: compile-verified (std-Go wasm + TinyGo wasm). The signaling state
// machine needs browser runtime validation; it is the foundation for a
// WebRTC-based skywire mesh transport (see docs/design/wasm-visor-p2p.md).
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
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

// webrtcSignalPort is the dmsg port the WebRTC signaling listener accepts on.
const webrtcSignalPort uint16 = 47

// wlog routes WebRTC signaling/ICE progress to a JS hook (window.__wrtclog) when
// present, so the control bridge / page can surface it. No-op otherwise.
func wlog(msg string) {
	if h := js.Global().Get("__wrtclog"); h.Truthy() {
		h.Invoke(msg)
	}
}

// signalMsg is one signaling message exchanged over the dmsg SignalChannel.
type signalMsg struct {
	Type      string `json:"type"`                // "offer" | "answer" | "candidate"
	SDP       string `json:"sdp,omitempty"`       // for offer/answer
	Candidate string `json:"candidate,omitempty"` // for an ICE candidate
	SDPMid    string `json:"sdpMid,omitempty"`
	SDPMLine  int    `json:"sdpMLineIndex,omitempty"`
}

// SignalChannel carries signaling messages between the two WebRTC peers before
// the direct DataChannel exists. In production it is a dmsg stream.
type SignalChannel interface {
	send(signalMsg) error
	recv() (signalMsg, error)
	Close() error
}

// dmsgSignalChannel frames signalMsgs as length-prefixed JSON over a dmsg stream.
type dmsgSignalChannel struct {
	s     *dmsg.Stream
	wmu   sync.Mutex
	hdr   [4]byte
	rdHdr [4]byte
}

func (c *dmsgSignalChannel) send(m signalMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	binary.BigEndian.PutUint32(c.hdr[:], uint32(len(b)))
	if _, err := c.s.Write(c.hdr[:]); err != nil {
		return err
	}
	_, err = c.s.Write(b)
	return err
}

func (c *dmsgSignalChannel) recv() (signalMsg, error) {
	if _, err := io.ReadFull(c.s, c.rdHdr[:]); err != nil {
		return signalMsg{}, err
	}
	n := binary.BigEndian.Uint32(c.rdHdr[:])
	if n > 1<<20 {
		return signalMsg{}, fmt.Errorf("signal frame too large: %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.s, b); err != nil {
		return signalMsg{}, err
	}
	var m signalMsg
	// NOTE: `return m, json.Unmarshal(b, &m)` is WRONG — Go leaves the order of
	// the result-value read vs the call unspecified, and TinyGo reads m (empty)
	// BEFORE Unmarshal populates it. Decode first, then return.
	err := json.Unmarshal(b, &m)
	return m, err
}

func (c *dmsgSignalChannel) Close() error { return c.s.Close() }

// dialWebRTC is the OFFERER: it creates the peer connection + DataChannel, sends
// the offer, applies the answer and remote candidates from sc, and returns once
// the DataChannel is open.
func dialWebRTC(ctx context.Context, sc SignalChannel, iceServers js.Value) (net.Conn, error) {
	wlog("dial: start")
	pc := newPeerConnection(iceServers)
	dc := pc.Call("createDataChannel", "skywire", map[string]interface{}{"ordered": true})
	conn := newWebRTCConn(dc)

	wireLocalCandidates(pc, sc)
	go pumpRemoteSignals(ctx, pc, sc, nil)

	offer, err := awaitJS(pc.Call("createOffer"))
	if err != nil {
		return nil, fmt.Errorf("createOffer: %w", err)
	}
	if _, err := awaitJS(pc.Call("setLocalDescription", offer)); err != nil {
		return nil, fmt.Errorf("setLocalDescription: %w", err)
	}
	if err := sc.send(signalMsg{Type: "offer", SDP: offer.Get("sdp").String()}); err != nil {
		return nil, fmt.Errorf("send offer: %w", err)
	}
	wlog("dial: offer sent, waiting for datachannel")
	if err := conn.waitOpen(ctx); err != nil {
		wlog("dial: waitOpen failed: " + err.Error())
		return nil, err
	}
	wlog("dial: datachannel OPEN")
	return conn, nil
}

// acceptWebRTC is the ANSWERER: it waits for the offer on sc, answers it, and
// returns the DataChannel (delivered via pc.ondatachannel) once it is open.
func acceptWebRTC(ctx context.Context, sc SignalChannel, iceServers js.Value) (net.Conn, error) {
	wlog("accept: start")
	pc := newPeerConnection(iceServers)
	dcCh := make(chan js.Value, 1)
	onDC := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		wlog("accept: ondatachannel fired")
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
		wlog("accept: answer sent")
		return sc.send(signalMsg{Type: "answer", SDP: answer.Get("sdp").String()})
	})

	select {
	case dc := <-dcCh:
		conn := newWebRTCConn(dc)
		if err := conn.waitOpen(ctx); err != nil {
			wlog("accept: waitOpen failed: " + err.Error())
			return nil, err
		}
		wlog("accept: datachannel OPEN")
		return conn, nil
	case <-ctx.Done():
		wlog("accept: ctx done before datachannel")
		return nil, ctx.Err()
	}
}

// newPeerConnection constructs an RTCPeerConnection with the given iceServers
// (a JS array, possibly null/undefined for none).
func newPeerConnection(iceServers js.Value) js.Value {
	cfg := js.Global().Get("Object").New()
	if iceServers.Truthy() {
		cfg.Set("iceServers", iceServers)
	}
	pc := js.Global().Get("RTCPeerConnection").New(cfg)
	pc.Set("oniceconnectionstatechange", js.FuncOf(func(js.Value, []js.Value) interface{} {
		wlog("iceConnectionState=" + pc.Get("iceConnectionState").String())
		return nil
	}))
	pc.Set("onconnectionstatechange", js.FuncOf(func(js.Value, []js.Value) interface{} {
		wlog("connectionState=" + pc.Get("connectionState").String())
		return nil
	}))
	return pc
}

// wireLocalCandidates forwards locally-gathered ICE candidates to the peer over
// the signaling channel (trickle ICE).
func wireLocalCandidates(pc js.Value, sc SignalChannel) {
	onCand := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		cand := args[0].Get("candidate")
		if !cand.Truthy() {
			wlog("local ICE gathering complete")
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
		if err := sc.send(signalMsg{
			Type:      "candidate",
			Candidate: cand.Get("candidate").String(),
			SDPMid:    mid,
			SDPMLine:  line,
		}); err != nil {
			wlog("send candidate err: " + err.Error())
		}
		return nil
	})
	pc.Set("onicecandidate", onCand)
}

// pumpRemoteSignals reads signaling messages from sc and applies them to pc.
// onOffer (answerer only) handles an inbound offer; for the offerer it is nil
// and an "answer" sets the remote description.
func pumpRemoteSignals(ctx context.Context, pc js.Value, sc SignalChannel, onOffer func(sdp string) error) {
	remoteSet := false
	var queued []signalMsg // candidates that arrived before the remote description
	addCand := func(m signalMsg) {
		cand := js.Global().Get("Object").New()
		cand.Set("candidate", m.Candidate)
		cand.Set("sdpMid", m.SDPMid)
		cand.Set("sdpMLineIndex", m.SDPMLine)
		if _, err := awaitJS(pc.Call("addIceCandidate", cand)); err != nil {
			wlog("addIceCandidate err: " + err.Error())
		}
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
			wlog("signal stream ended: " + err.Error())
			return
		}
		wlog("signal recv: " + m.Type)
		switch m.Type {
		case "offer":
			if onOffer != nil {
				if err := onOffer(m.SDP); err != nil {
					wlog("onOffer err: " + err.Error())
				} else {
					flush()
				}
			}
		case "answer":
			desc := map[string]interface{}{"type": "answer", "sdp": m.SDP}
			if _, err := awaitJS(pc.Call("setRemoteDescription", desc)); err != nil {
				wlog("setRemoteDescription(answer) err: " + err.Error())
			} else {
				flush()
			}
		case "candidate":
			// addIceCandidate fails if the remote description isn't set yet, so
			// buffer until it is (the classic trickle-ICE ordering fix).
			if remoteSet {
				addCand(m)
			} else {
				queued = append(queued, m)
			}
		}
	}
}

// webRTCConn adapts an RTCDataChannel to net.Conn (binaryType=arraybuffer),
// using the same buffer+notify+deadline pattern as the WebSocket/WebTransport
// browser conns.
type webRTCConn struct {
	dc   js.Value
	addr webrtcAddr

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

type webrtcAddr struct{}

func (webrtcAddr) Network() string { return "webrtc" }
func (webrtcAddr) String() string  { return "webrtc-datachannel" }

func newWebRTCConn(dc js.Value) *webRTCConn {
	dc.Set("binaryType", "arraybuffer")
	c := &webRTCConn{dc: dc, notify: make(chan struct{}), openCh: make(chan struct{})}

	onOpen := js.FuncOf(func(js.Value, []js.Value) interface{} {
		wlog("datachannel onopen")
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

	// A channel created already-open (rare, but createDataChannel can resolve
	// before we attach onopen) still reports readyState.
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
	return nil
}

func (c *webRTCConn) LocalAddr() net.Addr  { return c.addr }
func (c *webRTCConn) RemoteAddr() net.Addr { return c.addr }

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

// jsWebrtcListen(onConn) -> nil. Accepts inbound WebRTC signaling on dmsg port
// 47; for each, runs the answerer handshake and hands the resulting net.Conn to
// onConn(connHandle). The peer reaches us BY PK over dmsg, then we upgrade to a
// direct DataChannel — a browser p2p endpoint reachable through the mesh.
func jsWebrtcListen(_ js.Value, args []js.Value) interface{} {
	onConn := args[0]
	var iceServers js.Value
	if len(args) > 1 {
		iceServers = args[1]
	}
	if client == nil {
		return js.Global().Get("Error").New("not connected; call connect() first")
	}
	lis, err := client.Listen(webrtcSignalPort)
	if err != nil {
		return js.Global().Get("Error").New(err.Error())
	}
	go func() {
		for {
			str, err := lis.AcceptStream()
			if err != nil {
				return
			}
			go func(s *dmsg.Stream) {
				conn, err := acceptWebRTC(ctx, &dmsgSignalChannel{s: s}, iceServers)
				if err != nil {
					s.Close() //nolint:errcheck
					return
				}
				onConn.Invoke(netConnHandle(conn))
			}(str)
		}
	}()
	return nil
}

// jsWebrtcDial(remotePkHex) -> Promise<connHandle>. Opens a dmsg signaling
// stream to the peer's port 47, runs the offerer handshake, and resolves to the
// direct DataChannel net.Conn (wrapped as a JS handle).
func jsWebrtcDial(_ js.Value, args []js.Value) interface{} {
	remoteHex := args[0].String()
	var iceServers js.Value
	if len(args) > 1 {
		iceServers = args[1]
	}
	return promise(func() (interface{}, error) {
		if client == nil {
			return nil, errors.New("not connected; call connect() first")
		}
		var rPK cipher.PubKey
		if err := rPK.UnmarshalText([]byte(remoteHex)); err != nil {
			return nil, fmt.Errorf("bad remote public key: %w", err)
		}
		str, err := client.DialStream(ctx, dmsg.Addr{PK: rPK, Port: webrtcSignalPort})
		if err != nil {
			return nil, err
		}
		conn, err := dialWebRTC(ctx, &dmsgSignalChannel{s: str}, iceServers)
		if err != nil {
			str.Close() //nolint:errcheck
			return nil, err
		}
		return netConnHandle(conn), nil
	})
}

// netConnHandle wraps any net.Conn as a JS { send, onMessage, close } object,
// mirroring streamHandle but for the WebRTC DataChannel conn.
func netConnHandle(conn net.Conn) js.Value {
	obj := js.Global().Get("Object").New()
	obj.Set("send", js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		msg := a[0].String()
		go conn.Write([]byte(msg)) //nolint:errcheck
		return nil
	}))
	obj.Set("onMessage", js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		cb := a[0]
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					cb.Invoke(string(buf[:n]))
				}
				if err != nil {
					return
				}
			}
		}()
		return nil
	}))
	obj.Set("close", js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		conn.Close() //nolint:errcheck
		return nil
	}))
	return obj
}
