// Package visor pkg/visor/voice.go c3-vis-core
//
// Visor-side RPC surface for skychat 1:1 voice (the manager lives in
// pkg/skychat/voice, brought up by init_voice.go). Place a call, hang up, and
// list active calls; media rides an encrypted skywire transport.
package visor

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ErrVoiceDisabled is returned when the voice manager isn't running (voice
// needs a dmsg client; see init_voice.go).
var ErrVoiceDisabled = errors.New("voice: disabled (no dmsg)")

// VoiceCall places a 1:1 voice call to peer over the mesh and returns the new
// call id. Blocks until the callee accepts/declines or the dial times out; the
// media session then runs in the background until Hangup.
func (v *Visor) VoiceCall(peer cipher.PubKey) (string, error) {
	if v.voice == nil {
		return "", ErrVoiceDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess, err := v.voice.Call(ctx, peer)
	if err != nil {
		return "", err
	}
	return sess.CallID, nil
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
