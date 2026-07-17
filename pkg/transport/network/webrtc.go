// Package network pkg/transport/network/webrtc.go c2-net-transport
//
// WEBRTC is a first-class skywire transport type: a direct visor-to-visor link
// over a WebRTC DataChannel (DTLS+SCTP, NAT-traversed via ICE). Unlike WS/WT
// (which dial a single listening endpoint), WebRTC is genuinely peer-to-peer —
// the payload rides a direct encrypted pipe between the two leaves, no relay.
//
// WebRTC needs a signaling side-channel to exchange the SDP offer/answer and ICE
// candidates BEFORE the direct pipe exists. dmsg is that channel: the dialing
// visor opens a dmsg stream to the peer's signaling port (webrtcSignalPort) and
// the two run the offer/answer/ICE handshake over it (signalConn — 4-byte
// length-prefixed JSON, the SAME wire format the browser carrier uses, so a
// native pion visor and a browser visor interoperate). Once the DataChannel
// opens it is adapted to a net.Conn and flows through the SAME genericClient
// initTransport (Noise + yamux) path as every other carrier.
//
// The carrier is build-tagged: webrtc_native.go (pion) on native; the browser
// (TinyGo/js) dials the browser-native RTCPeerConnection. Both can dial AND
// accept (WebRTC is symmetric), unlike WS/WT where a browser can only dial.
package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// webrtcSignalPort is the dmsg port the WebRTC signaling listener accepts on.
// Sourced from the central skyenv registry — it used to hardcode 47 here, which
// collided with DmsgTransportSetupPort (47) and left webrtc dead fleet-wide
// (the listener couldn't bind; offers hit the transport-setup listener). Native
// and browser carriers share this constant so they interoperate.
const webrtcSignalPort = skyenv.DmsgWebRTCSignalPort

// signalMsg is one signaling message exchanged over the dmsg signaling stream.
// The JSON field set + names match the browser carrier exactly (interop).
type signalMsg struct {
	Type      string `json:"type"`                // "offer" | "answer" | "candidate"
	SDP       string `json:"sdp,omitempty"`       // for offer/answer
	Candidate string `json:"candidate,omitempty"` // for an ICE candidate
	SDPMid    string `json:"sdpMid,omitempty"`
	SDPMLine  int    `json:"sdpMLineIndex,omitempty"`
}

// signalConn frames signalMsgs as 4-byte big-endian length-prefixed JSON over an
// io.ReadWriteCloser (a dmsg stream in production, a net.Pipe in tests). send is
// safe for concurrent use: ICE candidates (from the OnICECandidate callback) and
// the offer/answer (from the pump) are sent from different goroutines, so the
// header+body pair must be written atomically.
type signalConn struct {
	rwc   io.ReadWriteCloser
	wmu   sync.Mutex
	hdr   [4]byte
	rdHdr [4]byte
}

func (c *signalConn) send(m signalMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	binary.BigEndian.PutUint32(c.hdr[:], uint32(len(b))) //nolint:gosec // len(b) of a marshaled signalMsg is far below 2^32
	if _, err := c.rwc.Write(c.hdr[:]); err != nil {
		return err
	}
	_, err = c.rwc.Write(b)
	return err
}

func (c *signalConn) recv() (signalMsg, error) {
	if _, err := io.ReadFull(c.rwc, c.rdHdr[:]); err != nil {
		return signalMsg{}, err
	}
	n := binary.BigEndian.Uint32(c.rdHdr[:])
	if n > 1<<20 {
		return signalMsg{}, fmt.Errorf("signal frame too large: %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.rwc, b); err != nil {
		return signalMsg{}, err
	}
	var m signalMsg
	err := json.Unmarshal(b, &m)
	return m, err
}

func (c *signalConn) Close() error { return c.rwc.Close() }

// webrtcClient is the WEBRTC-transport implementation of Client. It signals over
// the dmsg client (dialing/accepting webrtcSignalPort) and negotiates a direct
// DataChannel per peer.
type webrtcClient struct {
	*genericClient
	dmsgC   *dmsg.Client
	iceURLs []string // STUN/TURN URLs for ICE (skywire's own STUN; may be empty)
}

func newWebRTC(generic *genericClient, dmsgC *dmsg.Client, iceURLs []string) Client {
	client := &webrtcClient{genericClient: generic, dmsgC: dmsgC, iceURLs: iceURLs}
	client.netType = types.WEBRTC
	return client
}

// ErrWebRTCNoDmsg is returned when the WebRTC client has no dmsg client to signal
// over (signaling rides dmsg).
var ErrWebRTCNoDmsg = errors.New("webrtc: no dmsg client for signaling")

// Dial implements Client: open a dmsg signaling stream to the peer, run the
// offerer handshake (build-tagged: pion on native, the browser RTCPeerConnection
// under TinyGo/js), and wrap the negotiated DataChannel in a skywire transport.
func (c *webrtcClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if c.dmsgC == nil {
		return nil, ErrWebRTCNoDmsg
	}
	c.log.Debugf("Dialing WebRTC %v (signaling over dmsg:%d)", rPK, webrtcSignalPort)

	str, err := c.dialSignaling(ctx, rPK)
	if err != nil {
		return nil, err
	}
	c.log.Debugf("WebRTC signaling stream to %v established; starting offerer", rPK)
	conn, err := webrtcDial(ctx, str, c.iceURLs)
	if err != nil {
		str.Close() //nolint:errcheck,gosec
		return nil, err
	}

	tp, err := c.initTransport(ctx, conn, rPK, rPort)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return tp, nil
}

// webrtcSignalDialAttempts / webrtcSignalDialRetryDelay bound the retry of the
// dmsg signaling dial. A fresh-peer dmsg session is racy: the first DialStream
// can return ErrCannotConnectToDelegated ("dmsg error 202") before a session to
// the peer's delegated server is warm, then succeed moments later. Observed
// browser→native: the WebRTC transport failed on the first dial and succeeded on
// a manual retry every time.
const (
	webrtcSignalDialAttempts   = 4
	webrtcSignalDialRetryDelay = 400 * time.Millisecond
)

// dialSignaling opens the dmsg signaling stream to rPK's WebRTC port, retrying
// only the transient dmsg-202 (ErrCannotConnectToDelegated); any other error
// fails fast. Honors ctx cancellation between attempts.
func (c *webrtcClient) dialSignaling(ctx context.Context, rPK cipher.PubKey) (*dmsg.Stream, error) {
	var lastErr error
	for attempt := 1; attempt <= webrtcSignalDialAttempts; attempt++ {
		str, err := c.dmsgC.DialStream(ctx, dmsg.Addr{PK: rPK, Port: webrtcSignalPort})
		if err == nil {
			return str, nil
		}
		lastErr = err
		if !errors.Is(err, dmsg.ErrCannotConnectToDelegated) {
			break // not the transient 202 — don't burn retries on a real failure
		}
		c.log.Debugf("WebRTC signaling dial to %v: %v (attempt %d/%d, retrying)", rPK, err, attempt, webrtcSignalDialAttempts)
		if attempt < webrtcSignalDialAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(webrtcSignalDialRetryDelay):
			}
		}
	}
	return nil, fmt.Errorf("webrtc: dial signaling: %w", lastErr)
}

// Start implements Client: accept inbound WebRTC transports. It listens for
// signaling streams on the dmsg client's webrtcSignalPort; each one runs the
// answerer handshake (build-tagged: pion on native, the browser RTCPeerConnection
// under TinyGo/js) and the negotiated DataChannel net.Conn is delivered to the
// shared acceptTransports loop via webrtcListener. WebRTC is symmetric, so unlike
// WS/WT a browser visor can accept as well as dial.
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

// webrtcAddr is the net.Addr for a WebRTC DataChannel conn (both carriers).
type webrtcAddr struct{}

func (webrtcAddr) Network() string { return "webrtc" }
func (webrtcAddr) String() string  { return "webrtc-datachannel" }
