//go:build !tinygo

// Package network pkg/transport/network/webrtc_native.go
//
// Native WebRTC carrier via pion: dial (offerer) and accept (answerer) a direct
// DataChannel, negotiated over a dmsg signaling stream, adapted to a net.Conn.
// The browser carrier (TinyGo/js) speaks the SAME signaling wire format against
// the browser-native RTCPeerConnection, so a native pion visor and a browser
// visor interoperate.
package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v4"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// dcLabel is the DataChannel label both peers agree on (matches the browser).
const dcLabel = "skywire"

// dcReadBuf bounds a single SCTP message read off the detached DataChannel. pion
// returns ErrShortBuffer if a message exceeds the buffer, so it must be at least
// the largest frame the upper stack writes (Noise messages are <=64 KiB).
const dcReadBuf = 96 * 1024

// newWebRTCAPI builds a pion API with detached DataChannels (so we get an
// io.ReadWriteCloser to adapt to net.Conn instead of pion's callback model).
func newWebRTCAPI() *webrtc.API {
	se := webrtc.SettingEngine{}
	se.DetachDataChannels()
	return webrtc.NewAPI(webrtc.WithSettingEngine(se))
}

func webrtcConfig(iceURLs []string) webrtc.Configuration {
	cfg := webrtc.Configuration{}
	if len(iceURLs) > 0 {
		cfg.ICEServers = []webrtc.ICEServer{{URLs: iceURLs}}
	}
	return cfg
}

// wireLocalCandidates forwards locally-gathered ICE candidates to the peer over
// the signaling channel (trickle ICE).
func wireLocalCandidates(pc *webrtc.PeerConnection, sc *signalConn) {
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // nil candidate = gathering complete
		}
		init := c.ToJSON()
		mid := ""
		if init.SDPMid != nil {
			mid = *init.SDPMid
		}
		line := 0
		if init.SDPMLineIndex != nil {
			line = int(*init.SDPMLineIndex)
		}
		_ = sc.send(signalMsg{Type: "candidate", Candidate: init.Candidate, SDPMid: mid, SDPMLine: line}) //nolint:errcheck
	})
}

// pumpRemoteSignals reads signaling messages from sc and applies them to pc.
// onOffer (answerer only) handles an inbound offer; for the offerer it is nil and
// an "answer" sets the remote description. ICE candidates that arrive before the
// remote description is set are buffered (the trickle-ICE ordering fix).
func pumpRemoteSignals(ctx context.Context, pc *webrtc.PeerConnection, sc *signalConn, onOffer func(sdp string) error) {
	remoteSet := false
	var queued []signalMsg
	addCand := func(m signalMsg) {
		mid := m.SDPMid
		line := uint16(m.SDPMLine)                                                                                  //nolint:gosec
		_ = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate, SDPMid: &mid, SDPMLineIndex: &line}) //nolint:errcheck
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
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err == nil {
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
// offer over sc, apply the answer + remote candidates, and return once the
// DataChannel is open and detached to a net.Conn.
func webrtcDial(ctx context.Context, signal io.ReadWriteCloser, iceURLs []string) (net.Conn, error) {
	sc := &signalConn{rwc: signal}
	pc, err := newWebRTCAPI().NewPeerConnection(webrtcConfig(iceURLs))
	if err != nil {
		return nil, fmt.Errorf("webrtc: new peer connection: %w", err)
	}
	connCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)

	ordered := true
	dc, err := pc.CreateDataChannel(dcLabel, &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		pc.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("webrtc: create data channel: %w", err)
	}
	dc.OnOpen(func() {
		raw, derr := dc.Detach()
		if derr != nil {
			errCh <- derr
			return
		}
		connCh <- newDCConn(raw, pc, signal)
	})

	wireLocalCandidates(pc, sc)
	go pumpRemoteSignals(ctx, pc, sc, nil)

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("webrtc: create offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("webrtc: set local description: %w", err)
	}
	if err := sc.send(signalMsg{Type: "offer", SDP: offer.SDP}); err != nil {
		pc.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("webrtc: send offer: %w", err)
	}
	return awaitConn(ctx, pc, connCh, errCh)
}

// webrtcAccept is the ANSWERER: wait for the offer on sc, answer it, and return
// the DataChannel (delivered via pc.OnDataChannel) once open and detached.
func webrtcAccept(ctx context.Context, signal io.ReadWriteCloser, iceURLs []string) (net.Conn, error) {
	sc := &signalConn{rwc: signal}
	pc, err := newWebRTCAPI().NewPeerConnection(webrtcConfig(iceURLs))
	if err != nil {
		return nil, fmt.Errorf("webrtc: new peer connection: %w", err)
	}
	connCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			raw, derr := dc.Detach()
			if derr != nil {
				errCh <- derr
				return
			}
			connCh <- newDCConn(raw, pc, signal)
		})
	})

	wireLocalCandidates(pc, sc)
	go pumpRemoteSignals(ctx, pc, sc, func(offerSDP string) error {
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
			return err
		}
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		if err := pc.SetLocalDescription(answer); err != nil {
			return err
		}
		return sc.send(signalMsg{Type: "answer", SDP: answer.SDP})
	})

	return awaitConn(ctx, pc, connCh, errCh)
}

// awaitConn blocks until the DataChannel opens (connCh), errors (errCh), or ctx
// is done, cleaning up the peer connection on failure.
func awaitConn(ctx context.Context, pc *webrtc.PeerConnection, connCh chan net.Conn, errCh chan error) (net.Conn, error) {
	select {
	case conn := <-connCh:
		return conn, nil
	case err := <-errCh:
		pc.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("webrtc: data channel: %w", err)
	case <-ctx.Done():
		pc.Close() //nolint:errcheck,gosec
		return nil, ctx.Err()
	}
}

// dcConn adapts a detached pion DataChannel (datachannel.ReadWriteCloser, an
// SCTP message stream) to a net.Conn. A pump goroutine drains messages into a
// buffer; Read serves from it (deadline-aware); Write maps onto the channel.
// Close tears down the DataChannel, the peer connection, and the dmsg signaling
// stream together.
type dcConn struct {
	raw    datachannel.ReadWriteCloser
	pc     *webrtc.PeerConnection
	signal io.Closer

	mu        sync.Mutex
	buf       []byte
	notify    chan struct{}
	closed    bool
	closeErr  error
	rDeadline time.Time
}

func newDCConn(raw datachannel.ReadWriteCloser, pc *webrtc.PeerConnection, signal io.Closer) *dcConn {
	c := &dcConn{raw: raw, pc: pc, signal: signal, notify: make(chan struct{})}
	go c.readPump()
	return c
}

func (c *dcConn) readPump() {
	buf := make([]byte, dcReadBuf)
	for {
		n, err := c.raw.Read(buf)
		if n > 0 {
			b := make([]byte, n)
			copy(b, buf[:n])
			c.mu.Lock()
			c.buf = append(c.buf, b...)
			c.wake()
			c.mu.Unlock()
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *dcConn) wake() { close(c.notify); c.notify = make(chan struct{}) }

func (c *dcConn) fail(err error) {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.closeErr = err
		c.wake()
	}
	c.mu.Unlock()
}

func (c *dcConn) Read(p []byte) (int, error) {
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
				return 0, dcTimeout{}
			}
			t := time.NewTimer(d)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-notify:
		case <-timeout:
			return 0, dcTimeout{}
		}
	}
}

func (c *dcConn) Write(p []byte) (int, error) {
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
	return c.raw.Write(p)
}

func (c *dcConn) Close() error {
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
	c.raw.Close() //nolint:errcheck,gosec
	c.pc.Close()  //nolint:errcheck,gosec
	if c.signal != nil {
		c.signal.Close() //nolint:errcheck,gosec
	}
	return nil
}

func (c *dcConn) LocalAddr() net.Addr  { return webrtcAddr{} }
func (c *dcConn) RemoteAddr() net.Addr { return webrtcAddr{} }

func (c *dcConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.wake()
	c.mu.Unlock()
	return nil
}
func (c *dcConn) SetWriteDeadline(time.Time) error { return nil }
func (c *dcConn) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }

type webrtcAddr struct{}

func (webrtcAddr) Network() string { return "webrtc" }
func (webrtcAddr) String() string  { return "webrtc-datachannel" }

type dcTimeout struct{}

func (dcTimeout) Error() string   { return "webrtc: i/o timeout" }
func (dcTimeout) Timeout() bool   { return true }
func (dcTimeout) Temporary() bool { return true }

// Start implements Client: accept inbound WebRTC transports. It listens for
// signaling streams on the dmsg client's webrtcSignalPort; each one runs the
// answerer handshake and the negotiated DataChannel net.Conn is delivered to the
// shared acceptTransports loop via webrtcListener.
func (c *webrtcClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	if c.dmsgC == nil {
		return ErrWebRTCNoDmsg
	}
	go c.serve()
	return nil
}

func (c *webrtcClient) serve() {
	lis, err := newWebRTCListener(c.dmsgC, c.iceURLs)
	if err != nil {
		c.log.Errorf("Failed to start WebRTC signaling listener: %v", err)
		return
	}
	c.acceptTransports(lis)
}

// webrtcListener fronts the dmsg signaling listener as a net.Listener: each
// accepted signaling stream is answered and its DataChannel net.Conn delivered
// from Accept().
type webrtcListener struct {
	dmsgLis *dmsg.Listener
	iceURLs []string
	conns   chan net.Conn
	done    chan struct{}
	once    sync.Once
}

func newWebRTCListener(dmsgC *dmsg.Client, iceURLs []string) (*webrtcListener, error) {
	dl, err := dmsgC.Listen(webrtcSignalPort)
	if err != nil {
		return nil, err
	}
	l := &webrtcListener{
		dmsgLis: dl,
		iceURLs: iceURLs,
		conns:   make(chan net.Conn),
		done:    make(chan struct{}),
	}
	go l.acceptLoop()
	return l, nil
}

func (l *webrtcListener) acceptLoop() {
	for {
		str, err := l.dmsgLis.AcceptStream()
		if err != nil {
			return
		}
		go func(s *dmsg.Stream) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			conn, err := webrtcAccept(ctx, s, l.iceURLs)
			if err != nil {
				s.Close() //nolint:errcheck,gosec
				return
			}
			select {
			case l.conns <- conn:
			case <-l.done:
				conn.Close() //nolint:errcheck,gosec
			}
		}(str)
	}
}

// Accept implements net.Listener.
func (l *webrtcListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener.
func (l *webrtcListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.dmsgLis.Close() //nolint:errcheck,gosec
	})
	return nil
}

// Addr implements net.Listener.
func (l *webrtcListener) Addr() net.Addr { return webrtcAddr{} }
