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
