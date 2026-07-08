// Package visor pkg/visor/rpc_voice.go c3-vis-core
package visor

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// VoiceCall places a 1:1 voice call to the given peer PK; replies with the call id.
func (r *RPC) VoiceCall(peer *cipher.PubKey, callID *string) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceCall", peer)(callID, &err)
	if peer == nil {
		return fmt.Errorf("nil request")
	}
	id, err := r.visor.VoiceCall(*peer)
	if err != nil {
		return err
	}
	*callID = id
	return nil
}

// VoiceHangup ends an active call by id.
func (r *RPC) VoiceHangup(callID *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceHangup", callID)(nil, &err)
	if callID == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.VoiceHangup(*callID)
}

// VoiceActive replies with the ids of active calls.
func (r *RPC) VoiceActive(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceActive", nil)(out, &err)
	ids, err := r.visor.VoiceActive()
	if err != nil {
		return err
	}
	*out = ids
	return nil
}

// VoiceAnswer accepts a ringing inbound call by id.
func (r *RPC) VoiceAnswer(callID *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceAnswer", callID)(nil, &err)
	if callID == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.VoiceAnswer(*callID)
}

// VoiceDecline rejects a ringing inbound call by id.
func (r *RPC) VoiceDecline(callID *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceDecline", callID)(nil, &err)
	if callID == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.VoiceDecline(*callID)
}

// VoiceIncoming replies with the ringing inbound calls awaiting an answer.
func (r *RPC) VoiceIncoming(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceIncoming", nil)(out, &err)
	ids, err := r.visor.VoiceIncoming()
	if err != nil {
		return err
	}
	*out = ids
	return nil
}

// VoiceMuteReq toggles the mic/speaker mute of a call.
type VoiceMuteReq struct {
	CallID  string
	Mic     bool
	Speaker bool
}

// VoiceMute toggles the mic (send) and speaker (playback) mute of an active call.
func (r *RPC) VoiceMute(req *VoiceMuteReq, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceMute", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.VoiceMute(req.CallID, req.Mic, req.Speaker)
}

// VoiceAudioSnapshot is the recent sent/received PCM of an active call.
type VoiceAudioSnapshot struct {
	Sent []int16
	Recv []int16
}

// VoiceCallAudio replies with the recent sent + received PCM of a call.
func (r *RPC) VoiceCallAudio(callID *string, out *VoiceAudioSnapshot) (err error) {
	defer rpcutil.LogCall(r.log, "VoiceCallAudio", callID)(nil, &err)
	if callID == nil {
		return fmt.Errorf("nil request")
	}
	sent, recv, err := r.visor.VoiceCallAudio(*callID)
	if err != nil {
		return err
	}
	out.Sent, out.Recv = sent, recv
	return nil
}
