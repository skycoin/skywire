// Package voice pkg/skychat/voice/signal.go c2-app-chat
package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// SigType is a voice-signaling message kind.
type SigType uint8

const (
	// SigInvite offers a call (caller → callee).
	SigInvite SigType = iota + 1
	// SigAccept accepts an invite (callee → caller).
	SigAccept
	// SigDecline rejects an invite (callee → caller); Reason is human text.
	SigDecline
	// SigHangup ends an established call (either side).
	SigHangup
	// SigBusy signals the callee is already in a call.
	SigBusy
)

// Sig is one signaling message. It carries the call id, the sender, and — on
// Invite/Accept — the negotiated codec and the media port both sides will use
// for the RTP stream (identical over dmsg and skynet, per skyenv).
type Sig struct {
	Type      SigType       `json:"type"`
	CallID    string        `json:"call_id"`
	FromPK    cipher.PubKey `json:"from_pk"`
	Codec     string        `json:"codec,omitempty"`
	MediaPort uint16        `json:"media_port,omitempty"`
	Reason    string        `json:"reason,omitempty"`
}

const sigMaxLen = 64 << 10 // 64 KiB cap on a signaling frame

// writeSig writes one length-prefixed JSON signaling frame.
func writeSig(w io.Writer, s Sig) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(b) > sigMaxLen {
		return errors.New("voice: signaling frame too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b))) //nolint:gosec // len(b) is non-negative and capped at sigMaxLen
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// readSig reads one length-prefixed JSON signaling frame.
func readSig(r io.Reader) (Sig, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Sig{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > sigMaxLen {
		return Sig{}, fmt.Errorf("voice: bad signaling frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Sig{}, err
	}
	var s Sig
	if err := json.Unmarshal(buf, &s); err != nil {
		return Sig{}, err
	}
	return s, nil
}

// DialFunc opens a signaling/media stream to peer:port over a skywire network
// (a dmsg stream or a skynet route). The manager supplies one that tries the
// available networks; both use the SAME port (skyenv.SkychatVoiceSignalPort /
// SkychatVoiceMediaPort).
type DialFunc func(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error)

// InviteHandler is called when an inbound invite arrives. It receives the
// invite and the live signaling conn; the implementation decides to accept
// (reply SigAccept and set up media) or decline. It must not block long.
type InviteHandler func(inv Sig, conn net.Conn)

// Signaler runs the voice control channel. It ACCEPTS invites on one port over
// every skywire network it's given a listener for (dmsg + skynet — same port),
// and it DIALS invites out via DialFunc. Signaling frames are the length-
// prefixed JSON above.
type Signaler struct {
	localPK cipher.PubKey
	port    uint16
	dial    DialFunc
	log     *logging.Logger

	mu      sync.Mutex
	onInv   InviteHandler
	serving []net.Listener
}

// NewSignaler builds a Signaler bound to the given local PK / port / dialer.
func NewSignaler(localPK cipher.PubKey, port uint16, dial DialFunc, log *logging.Logger) *Signaler {
	if log == nil {
		log = logging.MustGetLogger("voice-signal")
	}
	return &Signaler{localPK: localPK, port: port, dial: dial, log: log}
}

// SetInviteHandler registers the inbound-invite callback.
func (s *Signaler) SetInviteHandler(h InviteHandler) {
	s.mu.Lock()
	s.onInv = h
	s.mu.Unlock()
}

// Serve accepts signaling connections on ALL provided listeners concurrently —
// pass the dmsg listener AND the skynet listener (both bound to s.port) so a
// caller reaches us over whichever network is up. Returns when ctx is done or
// every listener has failed.
func (s *Signaler) Serve(ctx context.Context, listeners ...net.Listener) {
	s.mu.Lock()
	s.serving = listeners
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, lis := range listeners {
		if lis == nil {
			continue
		}
		wg.Add(1)
		go func(lis net.Listener) {
			defer wg.Done()
			s.acceptLoop(ctx, lis)
		}(lis)
	}
	go func() { <-ctx.Done(); s.closeListeners() }()
	wg.Wait()
}

// AddListener starts accepting on an ADDITIONAL listener after Serve has already
// begun — e.g. the skynet networker registers later than the dmsg client, so its
// listener joins once it's up. Runs an accept loop until ctx is done.
func (s *Signaler) AddListener(ctx context.Context, lis net.Listener) {
	if lis == nil {
		return
	}
	s.mu.Lock()
	s.serving = append(s.serving, lis)
	s.mu.Unlock()
	go s.acceptLoop(ctx, lis)
}

func (s *Signaler) acceptLoop(ctx context.Context, lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.WithError(err).Debug("voice: signaling accept failed")
			return
		}
		go s.handleInbound(conn)
	}
}

func (s *Signaler) handleInbound(conn net.Conn) {
	sig, err := readSig(conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return
	}
	if sig.Type != SigInvite {
		// Only an invite legitimately opens a fresh signaling conn.
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: sig.CallID, FromPK: s.localPK, Reason: "expected invite"}) //nolint:errcheck
		_ = conn.Close()                                                                                            //nolint:errcheck
		return
	}
	s.mu.Lock()
	h := s.onInv
	s.mu.Unlock()
	if h == nil {
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: sig.CallID, FromPK: s.localPK, Reason: "voice not enabled"}) //nolint:errcheck
		_ = conn.Close()                                                                                              //nolint:errcheck
		return
	}
	h(sig, conn)
}

// Invite dials the peer's signaling port (over whichever network DialFunc
// resolves), sends an Invite, and returns the live conn + the peer's reply
// (SigAccept or SigDecline/SigBusy). On a non-accept reply it closes the conn
// and returns the reply with a nil conn.
func (s *Signaler) Invite(ctx context.Context, peer cipher.PubKey, callID, codec string, mediaPort uint16) (net.Conn, Sig, error) {
	conn, err := s.dial(ctx, peer, s.port)
	if err != nil {
		return nil, Sig{}, fmt.Errorf("voice: signaling dial: %w", err)
	}
	inv := Sig{Type: SigInvite, CallID: callID, FromPK: s.localPK, Codec: codec, MediaPort: mediaPort}
	if err := writeSig(conn, inv); err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, Sig{}, fmt.Errorf("voice: send invite: %w", err)
	}
	reply, err := readSig(conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, Sig{}, fmt.Errorf("voice: read invite reply: %w", err)
	}
	if reply.Type != SigAccept {
		_ = conn.Close() //nolint:errcheck
		return nil, reply, nil
	}
	return conn, reply, nil
}

func (s *Signaler) closeListeners() {
	s.mu.Lock()
	ls := s.serving
	s.serving = nil
	s.mu.Unlock()
	for _, l := range ls {
		if l != nil {
			_ = l.Close() //nolint:errcheck
		}
	}
}
