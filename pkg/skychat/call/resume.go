// Package call pkg/skychat/call/resume.go c4-app-chat
package call

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ResumeBudget is how long a call goes on trying to rebuild its media conn
// before it gives up and hangs up.
//
// A call's media is one stream on a SHARED dmsg session, so it does not
// survive that session on its own: a server evicting the client, a ping reaper,
// a phone moving from Wi-Fi to cellular — each takes the session down and every
// stream with it, and the call ended there. It ends here instead, only if the
// transport has not come back within this.
//
// Thirty seconds because both ends of the recovery are much faster: a dmsg
// client re-establishes a session within about fifteen (measured: 1.5s on a
// healthy link), and a Wi-Fi/cellular handover completes in five to fifteen.
// Longer would leave a dead call on screen; much shorter would give up during
// an ordinary handover, which is the case this exists for.
const ResumeBudget = 30 * time.Second

// resumeRetryInterval paces the re-dial attempts. The first one usually fails:
// the transport that carried the call has just died and its replacement is
// being dialed, so there is nothing to reach the peer over yet.
const resumeRetryInterval = 2 * time.Second

// resumeAttemptBudget caps ONE re-dial, so the whole budget is not spent
// inside the first. A dial to a peer there is currently no route to does not
// fail fast — it asks the setup nodes, falls back to a local search, tries to
// build a transport — and given the full thirty seconds it would take them,
// leaving the call one attempt where it should have had several. The retry
// matters more than the individual try here: what is being waited for is the
// local dmsg client finishing its own reconnect, and until it has, every
// attempt fails however long it is given.
const resumeAttemptBudget = 8 * time.Second

// resumeWait is a call whose media conn has failed and whose peer is expected
// to re-dial. Only the side that PLACED the call re-dials; the side that
// answered parks here, so the two cannot both dial and end up with a conn each.
type resumeWait struct {
	peer cipher.PubKey
	conn chan net.Conn // buffered(1)
}

// resumeOutbound rebuilds the media conn from the CALLER's side by re-dialing
// the peer, retrying until the budget runs out.
//
// The caller re-dials because somebody has to and only one of them may: two
// endpoints each dialing the other produce two media conns for one call, and
// then the question of which to keep. The caller already knows how to reach
// this peer, so it is the one that tries.
func (m *Manager) resumeOutbound(ctx context.Context, callID string, peer cipher.PubKey) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, ResumeBudget)
	defer cancel()

	ticker := time.NewTicker(resumeRetryInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, resumeAttemptBudget)
		conn, err := m.sig.Resume(attemptCtx, peer, callID)
		attemptCancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			// The last attempt's error, not the context's: "no route to the
			// peer" is what someone reading this needs, and "deadline
			// exceeded" is only how long we spent finding that out.
			return nil, fmt.Errorf("voice: gave up reconnecting after %s: %w", ResumeBudget, lastErr)
		case <-ticker.C:
		}
	}
}

// resumeInbound rebuilds the media conn from the CALLEE's side, by waiting for
// the caller to re-dial with this call's id.
func (m *Manager) resumeInbound(ctx context.Context, callID string, peer cipher.PubKey) (net.Conn, error) {
	w := &resumeWait{peer: peer, conn: make(chan net.Conn, 1)}
	m.mu.Lock()
	m.resuming[callID] = w
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.resuming, callID)
		m.mu.Unlock()
		// A conn that landed in the channel between our giving up and the
		// deregistration above has nobody left to read it. The buffered slot
		// would hold it open for the life of the process.
		select {
		case orphan := <-w.conn:
			_ = orphan.Close() //nolint:errcheck
		default:
		}
	}()

	timer := time.NewTimer(ResumeBudget)
	defer timer.Stop()
	select {
	case conn := <-w.conn:
		return conn, nil
	case <-timer.C:
		return nil, errors.New("voice: the caller did not reconnect")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handleResume is the callee side of a re-dial: it attaches the fresh conn to
// the call already waiting for one, or declines.
func (m *Manager) handleResume(sig Sig, conn net.Conn) {
	m.mu.Lock()
	w := m.resuming[sig.CallID]
	m.mu.Unlock()
	if w == nil {
		// No call of that id is missing its transport. Nothing to attach to,
		// and nothing worth telling the dialer beyond that.
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: sig.CallID, FromPK: m.cfg.LocalPK, Reason: "no call awaiting resume"}) //nolint:errcheck
		_ = conn.Close()                                                                                                        //nolint:errcheck
		return
	}
	if !m.resumeIsFromPeer(w, sig, conn) {
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: sig.CallID, FromPK: m.cfg.LocalPK, Reason: "resume from the wrong peer"}) //nolint:errcheck
		_ = conn.Close()                                                                                                           //nolint:errcheck
		m.log.WithField("call", sig.CallID).Warn("voice: refused a resume that did not come from the call's peer")
		return
	}
	// Accept BEFORE handing the conn over: the dialer starts sending media as
	// soon as it reads this, and the session must already own the conn by
	// then or those first frames land on nobody.
	ack := Sig{Type: SigAccept, CallID: sig.CallID, FromPK: m.cfg.LocalPK, Codec: m.cfg.Codec.Name(), MediaPort: m.cfg.SignalPort}
	if err := writeSig(conn, ack); err != nil {
		_ = conn.Close() //nolint:errcheck
		return
	}
	select {
	case w.conn <- conn:
	default:
		_ = conn.Close() //nolint:errcheck // already resumed by an earlier dial
	}
}

// peerNamed is a conn that can say who is at the other end from the transport's
// own handshake rather than from anything the peer asserts. dmsg streams and
// appnet's direct conns both can; a routed skynet conn cannot, which is why
// this is an optional interface and not a requirement.
type peerNamed interface {
	RemotePK() cipher.PubKey
}

// resumeIsFromPeer checks that a resume really comes from the party on the
// call.
//
// A resume attaches to a live call without ringing anyone, so it is worth more
// care than an invite, which at least ends up in front of a human. Where the
// carrier will say who it is talking to, that is what decides — it comes from
// the Noise handshake and a peer cannot claim otherwise. Where it will not, the
// check falls back to the asserted FromPK, which is the same footing every
// invite has always stood on, narrowed by a resume being possible only for a
// live call, only while its transport is down, and only with its 64 random bits
// of id.
func (m *Manager) resumeIsFromPeer(w *resumeWait, sig Sig, conn net.Conn) bool {
	if pn, ok := conn.(peerNamed); ok {
		return pn.RemotePK() == w.peer
	}
	return sig.FromPK == w.peer
}
