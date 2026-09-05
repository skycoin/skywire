//go:build !tinygo && !(js && wasm)

// Package network pkg/transport/network/webrtc_native.go c2-net-transport
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
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"

	"github.com/skycoin/skywire/pkg/logging"
)

// wrtcLog carries WebRTC signaling diagnostics. WebRTC negotiation is opaque
// otherwise — a failed dial just times out — so log the signaling milestones +
// the gathered/received ICE candidates. (Pion's On*StateChange callbacks have
// different signatures on native vs the js/wasm build, so deeper ICE-state
// logging can't live in this shared file.)
var wrtcLog = logging.MustGetLogger("webrtc")

// dcLabel is the DataChannel label both peers agree on (matches the browser).
const dcLabel = "skywire"

// dcReadBuf bounds a single SCTP message read off the detached DataChannel. pion
// returns ErrShortBuffer if a message exceeds the buffer, so it must be at least
// the largest frame the upper stack writes (Noise messages are <=64 KiB).
const dcReadBuf = 96 * 1024

// sctpReceiveBufferBytes is the SCTP receive-window (a_rwnd) this carrier
// advertises, raised from pion's 1 MiB default (pion/sctp initialRecvBufSize).
// Throughput over a reliable stream is bounded by window / RTT (the
// bandwidth-delay product): the advertised receiver-window credit caps how much
// unacknowledged data may be in flight. The skywire mesh path has a high
// end-to-end RTT (~0.5-1 s across relays), so the 1 MiB default throttles a
// single DataChannel to ~1 MB/s regardless of the underlying link — and under a
// full window SCTP flow-control simply stops advancing, which presents as a
// silent mid-transfer stall (the same class of bug as the 256 KB yamux window,
// #4211). 16 MiB gives 16 MB/s at 1 s RTT (128 Mbps), saturating a 100 Mbps card
// with headroom; SCTP only holds up to a window's worth of actually-received-
// but-unread data, so idle channels pay nothing. The advertised window governs
// the peer's send rate INTO us, so this value speeds our downloads; the peer's
// value speeds our uploads once the fleet redeploys. Fits uint32 (a_rwnd is a
// 32-bit credit) with room to spare.
const sctpReceiveBufferBytes = 16 * 1024 * 1024

// newWebRTCAPI builds a pion API with detached DataChannels (so we get an
// io.ReadWriteCloser to adapt to net.Conn instead of pion's callback model).
func newWebRTCAPI() *webrtc.API {
	se := webrtc.SettingEngine{}
	se.DetachDataChannels()
	// Raise the SCTP receive window off its 1 MiB default so a high-BDP mesh path
	// isn't flow-control throttled/stalled — see sctpReceiveBufferBytes.
	se.SetSCTPMaxReceiveBufferSize(sctpReceiveBufferBytes)
	// mDNS is the single largest per-PeerConnection cost. pion/ice creates an
	// mDNS Conn whenever the mode is anything but Disabled, and an unset mode
	// defaults to QueryOnly (ice/agent.go setupMDNSConfig), so we were paying for
	// it without ever asking: ~14 goroutines per PeerConnection (readLoop, v4/v6
	// packet conns, sweepLoop, start.funcN). Measured on a host visor with 460
	// live PeerConnections: 6,960 goroutines, 38% of the entire process.
	//
	// Skywire peers are addressed by public key and exchange ICE candidates over
	// a dmsg signaling stream, so we never need to resolve a peer's ".local"
	// name. Disabling also makes our own host candidates plain IPs, which is what
	// a visor with a routable address wants anyway.
	//
	// TRADE-OFF: Disabled also DISCARDS remote mDNS candidates. Browsers obfuscate
	// private host IPs as ".local", so a browser (wasm) visor on the SAME LAN as a
	// native visor loses that direct path and must fall back to a STUN-derived
	// srflx candidate — and iceURLs may be empty, in which case such a pair has no
	// candidate left at all. Loopback is unaffected (browsers do not obfuscate
	// 127.0.0.1), so the local dev loop keeps working. Set SKYWIRE_WEBRTC_MDNS=1
	// to restore pion's QueryOnly default if a deployment needs same-LAN browser
	// peers without STUN.
	if !mdnsEnabled() {
		se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	}
	// Empty MediaEngine: skywire uses WebRTC for DATA CHANNELS ONLY, never audio/
	// video, so registering no codecs keeps the negotiated SDP media-free.
	//
	// This alone does NOT stop pion's media goroutines, which is what the earlier
	// version of this comment claimed. NewAPI only skips the DEFAULT interceptors
	// when an interceptor registry is supplied — with just WithMediaEngine it
	// still calls RegisterDefaultInterceptorsWithOptions (webrtc/api.go), so every
	// PeerConnection kept spawning twcc + sender/receiver report loops: 1,856
	// goroutines (10%) on the same host. An empty registry removes them.
	//
	// The SRTP/SRTCP sessions (another ~10%) are NOT reachable from here: pion
	// calls startSRTP() unconditionally once DTLS connects (dtlstransport.go), so
	// suppressing those needs an upstream change, not a local one.
	return webrtc.NewAPI(
		webrtc.WithSettingEngine(se),
		webrtc.WithMediaEngine(&webrtc.MediaEngine{}),
		webrtc.WithInterceptorRegistry(&interceptor.Registry{}),
	)
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
		wrtcLog.Debugf("webrtc: gathered local candidate: %s", init.Candidate)
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
			wrtcLog.Info("webrtc[answerer]: received offer")
			if onOffer != nil {
				if err := onOffer(m.SDP); err != nil {
					wrtcLog.WithError(err).Warn("webrtc[answerer]: onOffer failed")
				} else {
					flush()
				}
			}
		case "answer":
			wrtcLog.Info("webrtc[offerer]: received answer")
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
				wrtcLog.WithError(err).Warn("webrtc[offerer]: set remote (answer) failed")
			} else {
				flush()
			}
		case "candidate":
			wrtcLog.Debugf("webrtc: received remote candidate (remoteSet=%t): %s", remoteSet, m.Candidate)
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
	// Surface a dead path promptly. pion's ICE agent already runs consent-freshness
	// keepalive (a STUN binding every ~2 s, "disconnected" after ~5 s of silence,
	// "failed" after ~25 s more) — but that liveness verdict was never wired to the
	// net.Conn, so a DataChannel whose path had died left dcConn.Read blocked on
	// raw.Read indefinitely (a SILENT stall the mux's route-group liveness could not
	// evict). Fail the conn as soon as the PeerConnection reaches Failed or Closed so
	// the stall trips a read error immediately, without adding a redundant app-level
	// ping on top of ICE's. (A flow-control stall — the BDP trap Lever 1 addresses —
	// keeps packets flowing, so ICE stays Connected; that path is handled by the
	// larger receive window and by write-deadline forwarding below.)
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			c.fail(fmt.Errorf("webrtc: peer connection %s", s))
		}
	})
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

// rawConnDetails contributes the webrtc-specific connection metadata to
// a ConnDetails: the selected ICE candidate pair's local/remote
// endpoints (the addresses the DataChannel actually flows between —
// dcConn.RemoteAddr() is only the opaque "webrtc-datachannel"
// placeholder). Best-effort: before the pair is nominated (or if pion
// can't report it) the placeholder is left in place. The DTLS
// fingerprint is not exposed by pion's public API and is left as a
// known gap. Called by transport.ConnDetails via the pre-noise rawConn.
func (c *dcConn) rawConnDetails(d *ConnDetails) {
	if c.pc == nil {
		return
	}
	sctp := c.pc.SCTP()
	if sctp == nil {
		return
	}
	dtls := sctp.Transport()
	if dtls == nil {
		return
	}
	ice := dtls.ICETransport()
	if ice == nil {
		return
	}
	pair, err := ice.GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return
	}
	if pair.Remote != nil {
		d.RemoteAddr = net.JoinHostPort(pair.Remote.Address, strconv.Itoa(int(pair.Remote.Port)))
	}
	if pair.Local != nil {
		d.LocalAddr = net.JoinHostPort(pair.Local.Address, strconv.Itoa(int(pair.Local.Port)))
	}
}

func (c *dcConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.wake()
	c.mu.Unlock()
	return nil
}

// SetWriteDeadline forwards to the underlying SCTP stream when it supports
// deadlines (pion's detached DataChannel is a datachannel.ReadWriteCloserDeadliner).
// Writes otherwise block in raw.Write with no bound, so a wedged channel — the
// receiver stalled and the SCTP send window exhausted — hangs the writer forever
// and the upper stack's own write timeout can never trip. Honoring the deadline
// lets a stall surface as a write timeout at the caller's chosen deadline. Reads
// keep their dcConn-layer deadline (the readPump owns raw.Read), so only the write
// side is forwarded here.
func (c *dcConn) SetWriteDeadline(t time.Time) error {
	if d, ok := c.raw.(datachannel.ReadWriteCloserDeadliner); ok {
		return d.SetWriteDeadline(t)
	}
	return nil
}
func (c *dcConn) SetDeadline(t time.Time) error {
	werr := c.SetWriteDeadline(t)
	rerr := c.SetReadDeadline(t)
	if rerr != nil {
		return rerr
	}
	return werr
}

type dcTimeout struct{}

func (dcTimeout) Error() string   { return "webrtc: i/o timeout" }
func (dcTimeout) Timeout() bool   { return true }
func (dcTimeout) Temporary() bool { return true }

// mdnsEnabled reports whether pion's mDNS ICE machinery should be left on.
// Default off — see newWebRTCAPI for the goroutine cost and the same-LAN
// browser-peer trade-off that SKYWIRE_WEBRTC_MDNS=1 buys back.
func mdnsEnabled() bool {
	switch os.Getenv("SKYWIRE_WEBRTC_MDNS") {
	case "1", "true", "TRUE", "yes":
		return true
	default:
		return false
	}
}
