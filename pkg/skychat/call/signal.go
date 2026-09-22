// Package call pkg/skychat/call/signal.go c4-app-chat
package call

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
	// SigResume re-attaches a media conn to a call already in progress whose
	// transport died under it. Appended last on purpose: a peer that predates
	// it reads an unknown type, answers SigDecline("expected invite"), and the
	// call ends the way it used to instead of misbehaving.
	SigResume
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

// ResumeHandler is called when a peer re-dials to continue a call whose media
// transport failed. It receives the resume frame and the fresh conn; the
// implementation answers SigAccept and hands the conn to the live session, or
// declines if it knows of no such call awaiting one.
type ResumeHandler func(sig Sig, conn net.Conn)

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

	mu       sync.Mutex
	onInv    InviteHandler
	onResume ResumeHandler
	serving  []net.Listener
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

// SetResumeHandler registers the callback for a peer re-dialing an existing
// call. Leaving it unset makes this endpoint answer every resume with a
// decline, which is what a build without call resumption should do.
func (s *Signaler) SetResumeHandler(h ResumeHandler) {
	s.mu.Lock()
	s.onResume = h
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
	if sig.Type == SigResume {
		s.mu.Lock()
		h := s.onResume
		s.mu.Unlock()
		if h == nil {
			_ = writeSig(conn, Sig{Type: SigDecline, CallID: sig.CallID, FromPK: s.localPK, Reason: "resume not supported"}) //nolint:errcheck
			_ = conn.Close()                                                                                                 //nolint:errcheck
			return
		}
		h(sig, conn)
		return
	}
	if sig.Type != SigInvite {
		// Only an invite or a resume legitimately opens a fresh signaling conn.
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
	reply, err := awaitReply(ctx, conn, func(c net.Conn) { s.cancelInvite(c, callID) })
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

// awaitReply waits for one signaling frame on a conn we have just written a
// request to, bounded by ctx, and hands the conn back ready to carry media.
//
// The bounding is both halves on purpose. Only the dial used to take the
// context and the read then blocked for as long as the peer cared to ring: a
// caller asking for a thirty-second call waited fifty-six, and nothing it did
// — deadline, cancel, hang up — ended it. The deadline covers a peer that
// never replies; onGiveUp covers a caller who gives up first, because a
// deadline already set cannot be brought forward.
//
// On success the read deadline is CLEARED. Past this point the conn is the
// media conn, and a call would otherwise end the moment the request's budget
// elapsed.
func awaitReply(ctx context.Context, conn net.Conn, onGiveUp func(net.Conn)) (Sig, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl) //nolint:errcheck // not every conn honors one; onGiveUp still does
	}
	stop := context.AfterFunc(ctx, func() { onGiveUp(conn) })
	reply, err := readSig(conn)
	stop()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The read error here is only ever "closed" — onGiveUp did that.
			// The context is the actual cause and the only useful thing to
			// report. No "voice:" prefix: both callers add their own.
			return Sig{}, fmt.Errorf("no answer: %w", ctxErr)
		}
		return Sig{}, err
	}
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	return reply, nil
}

// Resume re-dials the peer's signaling port to continue an EXISTING call whose
// media transport died, and returns the fresh media conn.
//
// It carries the original call id, which is how the peer tells this apart from
// a new call: a resume must attach to the session already running rather than
// ring anybody. Same shape as Invite otherwise — request, bounded wait, and on
// SigAccept the conn becomes the media conn.
func (s *Signaler) Resume(ctx context.Context, peer cipher.PubKey, callID string) (net.Conn, error) {
	conn, err := s.dial(ctx, peer, s.port)
	if err != nil {
		return nil, fmt.Errorf("voice: resume dial: %w", err)
	}
	if err := writeSig(conn, Sig{Type: SigResume, CallID: callID, FromPK: s.localPK}); err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("voice: send resume: %w", err)
	}
	reply, err := awaitReply(ctx, conn, func(c net.Conn) { _ = c.Close() }) //nolint:errcheck // best effort; the read below reports the outcome
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("voice: resume reply: %w", err)
	}
	if reply.Type != SigAccept {
		_ = conn.Close() //nolint:errcheck
		reason := reply.Reason
		if reason == "" {
			reason = sigTypeName(reply.Type)
		}
		return nil, fmt.Errorf("voice: resume refused: %s", reason)
	}
	return conn, nil
}

// hangupWriteGrace bounds the farewell below. A grace rather than a write
// deadline because a deadline is a no-op on some of the carriers voice runs
// over (appnet's directConn sets none), so the only way to be sure the conn
// gets dropped is to stop waiting on the write rather than to bound it.
const hangupWriteGrace = 500 * time.Millisecond

// cancelInvite ends an invite this side has given up on — the caller hanging
// up while the callee is still ringing, or the ring budget running out.
//
// Closing the conn is what ends the call, and it stays the thing that does.
// The frame in front of it is so the callee learns WHY, and learns it without
// having to wait on a carrier propagating a half-close: the callee reads this
// conn throughout the ring precisely so a cancel can stop the ringing at once
// (see ringWatch). The write is best-effort and strictly bounded, because the
// close must happen whatever it does — otherwise hanging up during a ring
// would itself hang, on a conn that is already wedged.
func (s *Signaler) cancelInvite(conn net.Conn, callID string) {
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_ = writeSig(conn, Sig{Type: SigHangup, CallID: callID, FromPK: s.localPK, Reason: "caller hung up"}) //nolint:errcheck
	}()
	timer := time.NewTimer(hangupWriteGrace)
	defer timer.Stop()
	select {
	case <-sent:
	case <-timer.C:
	}
	_ = conn.Close() //nolint:errcheck // the close is the point; a conn already gone reports so
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
