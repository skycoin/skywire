// Package visor pkg/visor/voice.go c3-vis-core
//
// Visor-side RPC surface for skychat 1:1 voice (the manager lives in
// pkg/skychat/call, brought up by init_voice.go). Place a call, hang up, and
// list active calls; media rides an encrypted skywire transport.
package visor

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	skycall "github.com/skycoin/skywire/pkg/skychat/call"
)

// ErrVoiceDisabled is returned when the voice manager isn't running (voice
// needs a dmsg client; see init_voice.go).
var ErrVoiceDisabled = errors.New("voice: disabled (no dmsg)")

// voiceDialBudget is how long a caller waits for an answer: the whole of the
// callee's ring, plus room for the reply to come back.
//
// Derived rather than chosen, because the two used to be independent numbers
// that disagreed — a 30s dial against a 45s ring. The caller gave up first and
// the callee went on ringing for fifteen seconds for a call that no longer
// had anyone at the other end, which is a phone ringing at nobody.
const voiceDialBudget = skycall.RingTimeout + 10*time.Second

// VoiceCall places a 1:1 voice call to peer over the mesh and returns the new
// call id. Blocks until the callee accepts or declines, or until the ring
// budget elapses; the media session then runs in the background until Hangup.
func (v *Visor) VoiceCall(peer cipher.PubKey) (string, error) {
	if v.voice == nil {
		return "", ErrVoiceDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), voiceDialBudget)
	defer cancel()
	sess, err := v.voice.Call(ctx, peer)
	if err != nil {
		return "", err
	}
	return sess.CallID, nil
}

// VoiceDial places a 1:1 voice call and returns its id at once, without
// waiting for an answer. The ring then runs in the background and the caller
// follows it through VoiceDialing / VoiceActive, canceling with VoiceHangup.
//
// This is the one an HTTP handler wants: both surfaces that place calls cap a
// request below the ring budget, so a blocking VoiceCall could never return
// the id it produced. See call.Manager.Dial.
func (v *Visor) VoiceDial(peer cipher.PubKey) (string, error) {
	if v.voice == nil {
		return "", ErrVoiceDisabled
	}
	return v.voice.Dial(peer, voiceDialBudget), nil
}

// VoiceHangup ends an active call by id.
func (v *Visor) VoiceHangup(callID string) error {
	if v.voice == nil {
		return ErrVoiceDisabled
	}
	return v.voice.Hangup(callID)
}

// VoiceActive returns the ids of active calls.
func (v *Visor) VoiceActive() ([]string, error) {
	if v.voice == nil {
		return nil, ErrVoiceDisabled
	}
	return v.voice.Active(), nil
}

// VoiceAnswer accepts a ringing inbound call by id (explicit-answer mode, when
// real audio is enabled).
func (v *Visor) VoiceAnswer(callID string) error {
	if v.voice == nil {
		return ErrVoiceDisabled
	}
	return v.voice.Answer(callID)
}

// VoiceDecline rejects a ringing inbound call by id.
func (v *Visor) VoiceDecline(callID string) error {
	if v.voice == nil {
		return ErrVoiceDisabled
	}
	return v.voice.Decline(callID)
}

// VoiceIncoming returns the ringing inbound calls awaiting an answer, each
// formatted as "<call-id> from <peer-pk>".
func (v *Visor) VoiceIncoming() ([]string, error) {
	if v.voice == nil {
		return nil, ErrVoiceDisabled
	}
	var out []string
	for _, inv := range v.voice.Incoming() {
		out = append(out, inv.CallID+" from "+inv.FromPK.Hex())
	}
	return out, nil
}

// VoiceDialingInfo is one call this visor is placing, before it is answered.
type VoiceDialingInfo struct {
	CallID string `json:"call_id"`
	Peer   string `json:"peer"`
}

// VoiceDialing returns the calls being placed right now.
//
// It was deliberately kept OFF the API interface, on the grounds that a
// caller's own dial state is of no use to a remote operator. True, but it is
// of use to the UI beside this visor, and skychat reaches the visor through
// this interface — so the one surface that most needs it was the one that
// could not have it. The consequence was not cosmetic: an outbound call that
// is still ringing has no id anywhere the desktop UI can see, and the hang-up
// button works off an id, so a call could be placed and not called off. The
// phone could, through the hypervisor's own route; the desktop could not.
//
// The hypervisor route still answers empty for a REMOTE visor, so what a
// remote operator can see is unchanged by this.
func (v *Visor) VoiceDialing() ([]VoiceDialingInfo, error) {
	if v.voice == nil {
		return nil, ErrVoiceDisabled
	}
	out := make([]VoiceDialingInfo, 0)
	for _, d := range v.voice.Dialing() {
		out = append(out, VoiceDialingInfo{CallID: d.CallID, Peer: d.Peer.Hex()})
	}
	return out, nil
}

// VoiceCallAudio returns the most recent buffered sent + received PCM for an
// active call (only when the visor runs with real audio, which taps call audio).
// The CLI polls this to draw a live two-panel spectrogram.
func (v *Visor) VoiceCallAudio(callID string) (sent, recv []int16, err error) {
	if v.voice == nil {
		return nil, nil, ErrVoiceDisabled
	}
	return v.voice.CallAudio(callID)
}

// VoiceMute toggles the mic (send) and speaker (playback) mute of an active
// call. mic silences what the peer hears from us; speaker silences what we hear
// from the peer.
func (v *Visor) VoiceMute(callID string, mic, speaker bool) error {
	if v.voice == nil {
		return ErrVoiceDisabled
	}
	return v.voice.SetMute(callID, mic, speaker)
}
