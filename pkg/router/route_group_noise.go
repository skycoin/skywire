// Package router pkg/router/route_group_noise.go c2-net-routing
package router

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/routing"
)

// perFrameNoiseCap returns CapPerFrameNoise when we want per-frame noise and the
// group is encrypted (per-frame noise IS the encryption); 0 otherwise.
// perFrameNoiseCap reports the CapPerFrameNoise bit iff this side wants per-frame
// noise AND the handshake being sent is encrypted. It takes `encrypt` as an
// argument rather than reading rg.encrypt because rg.encrypt is only set when a
// handshake is RECEIVED (handlePacket) — on the INITIATOR's first send (from
// saveRouteGroupRules, before it has received anything) rg.encrypt is still its
// zero value (false). Gating on the field there would strip the cap + noise
// message from the initiator's msg1, so the responder never sees per-frame
// offered and the whole group silently falls back to stream noise. sendHandshake
// already carries the correct encrypt value as its argument (initiator: always
// true; responder: rg.encrypt, which is set by then).
func (rg *RouteGroup) perFrameNoiseCap(encrypt bool) uint16 {
	if rg.perFrameNoiseWant && encrypt {
		return routing.CapPerFrameNoise
	}
	return 0
}

// nextPerFrameNoiseMsg returns the noise KK message to piggyback on THIS
// handshake send, creating the session on first use. Idempotent across
// retransmits: once produced, the same bytes are cached and returned so a resend
// never advances the noise state machine. Initiator: first call builds ns and
// produces msg1. Responder: ns was created + fed msg1 in handlePacket, so the
// first call here produces msg2 and completes the responder cipher. Called with
// rg.mu held.
func (rg *RouteGroup) nextPerFrameNoiseMsg() ([]byte, error) {
	if rg.perFrameNoiseMsg != nil {
		return rg.perFrameNoiseMsg, nil
	}
	if rg.ns == nil {
		// Only the KK INITIATOR opens the session here (it writes msg1 first).
		// A responder must READ the initiator's msg1 before it can write msg2 —
		// that happens in handlePacket, which creates the session. Until then a
		// responder has nothing to piggyback, so return no message rather than
		// call MakeHandshakeMessage out of turn (which the noise state machine
		// rejects: "unexpected call to WriteMessage should be ReadMessage").
		if !rg.nsConf.Initiator {
			return nil, nil
		}
		ns, err := noise.New(noise.HandshakeKK, rg.nsConf)
		if err != nil {
			return nil, err
		}
		rg.ns = ns
	}
	// Never write out of turn: only produce a message when the noise pattern says
	// it is ours to write. A resend of an already-produced message returns the
	// cached bytes above, so this only gates the FIRST production of each message.
	if !rg.ns.WriterTurn() {
		return nil, nil
	}
	msg, err := rg.ns.MakeHandshakeMessage()
	if err != nil {
		return nil, err
	}
	rg.perFrameNoiseMsg = msg
	if rg.ns.HandshakeFinished() {
		rg.wirePerFrameNoise()
	}
	return msg, nil
}

// wirePerFrameNoise installs the per-frame seal/open onto the mux and marks the
// group active, exactly once, the moment the noise transport cipher is ready.
// The seal/open use the mux frame SEQUENCE as the AEAD nonce; the cipher's
// Encrypt/Decrypt take an explicit nonce and are stateless (no per-call mutation),
// so concurrent per-leg send/recv is safe. Called with rg.mu held.
func (rg *RouteGroup) wirePerFrameNoise() {
	rg.perFrameNoiseOnce.Do(func() {
		ns := rg.ns
		if ns == nil || rg.mux == nil {
			return
		}
		rg.mux.seal = func(seq uint32, plaintext []byte) []byte {
			return ns.SealWithNonce(uint64(seq), plaintext)
		}
		rg.mux.open = func(seq uint32, ciphertext []byte) ([]byte, error) {
			return ns.OpenWithNonce(uint64(seq), ciphertext)
		}
		rg.perFrameNoiseActive = true
		rg.logger.Debug("Per-frame noise active: mux seal/open wired, EncryptConn bypassed")
	})
}

func (rg *RouteGroup) sendHandshake(encrypt bool) error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), closeRoutineTimeout)
	defer cancel()

	for i := 0; i < len(rg.tps); i++ {
		tp := rg.tps[i]

		if tp == nil {
			continue
		}

		rule := rg.fwd[i]
		// CapFEC is advertised only when this group's config opts in (router
		// Config.MuxFEC, visor config routing.mux_fec): FEC repair frames are a
		// flat 25%+ wire overhead on every multi-leg group and the legs ride
		// reliable transports whose gaps SACK recovery refills, so it is off by
		// default. It still only ACTIVATES when the peer also advertises it, so an
		// old or non-opted peer simply never negotiates it and is unaffected.
		caps := muxHandshakeCaps() | routing.CapLegState | routing.CapUniDir | routing.CapLegRehome
		if DeliveryCRCAdvertised() {
			caps |= routing.CapDeliveryCRC
		}
		if rg.cfg != nil && rg.cfg.FEC {
			caps |= routing.CapFEC
		}
		var packet routing.Packet
		if pfn := rg.perFrameNoiseCap(encrypt); pfn != 0 {
			msg, mErr := rg.nextPerFrameNoiseMsg()
			if mErr != nil {
				// Never silently drop to plaintext: if the per-frame noise
				// message can't be produced, abort this handshake rather than
				// advertise a capability we can't honor.
				return fmt.Errorf("per-frame noise handshake message: %w", mErr)
			}
			if msg != nil {
				packet = routing.MakeHandshakePacketWithNoise(rule.NextRouteID(), encrypt, caps|pfn, msg)
			} else {
				// No per-frame message to carry yet (responder that has not read
				// the initiator's msg1). Send a plain handshake WITHOUT the
				// CapPerFrameNoise bit this round — advertising it without a
				// message would make the peer try to open a nil handshake. The
				// responder emits msg2 (with the cap) from handlePacket once it
				// has processed the forward handshake; the initiator retransmits
				// until then.
				packet = routing.MakeHandshakePacket(rule.NextRouteID(), encrypt, caps)
			}
		} else {
			packet = routing.MakeHandshakePacket(rule.NextRouteID(), encrypt, caps)
		}

		err := rg.writePacket(ctx, tp, packet, rule.KeyRouteID())
		if err == nil {
			rg.logger.Debugf("Sent handshake via transport %v", tp.Entry.ID)
			return nil
		}

		rg.logger.Debugf("Failed to send handshake via transport %v: %v [%v/%v]",
			tp.Entry.ID, err, i+1, len(rg.tps))
	}

	return ErrNoSuitableTransport
}
