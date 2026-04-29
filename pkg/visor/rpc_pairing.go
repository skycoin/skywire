// Package visor pkg/visor/rpc_pairing.go
//
// RPC adapter for the chat-pair feed manager. Thin wrappers over the
// Visor methods in pairing.go.
package visor

import (
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// PairAddRequest is the input to RPC.PairAdd.
type PairAddRequest struct {
	PeerPK cipher.PubKey `json:"peer_pk"`
}

// PairSendRequest is the input to RPC.PairSend.
type PairSendRequest struct {
	PeerPK cipher.PubKey `json:"peer_pk"`
	Text   string        `json:"text"`
}

// PairPollRequest is the input to RPC.PairPoll.
type PairPollRequest struct {
	// Since is the lower bound (exclusive). Pass time.Time{} (zero)
	// to retrieve the entire current inbox window.
	Since time.Time `json:"since"`
}

// PairAdd creates a pair record and brings up the local publisher /
// subscriber.
func (r *RPC) PairAdd(req *PairAddRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "PairAdd", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.PairAdd(req.PeerPK)
}

// PairList returns every persisted pair (pending / active / revoked).
func (r *RPC) PairList(_ *struct{}, out *[]PairInfo) (err error) {
	defer rpcutil.LogCall(r.log, "PairList", nil)(out, &err)
	pairs, err := r.visor.PairList()
	if err != nil {
		return err
	}
	*out = pairs
	return nil
}

// PairRemove tears down the pair and marks the record revoked.
func (r *RPC) PairRemove(peerPK *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "PairRemove", peerPK)(nil, &err)
	if peerPK == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.PairRemove(*peerPK)
}

// PairMarkActive promotes a pending pair to active.
func (r *RPC) PairMarkActive(peerPK *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "PairMarkActive", peerPK)(nil, &err)
	if peerPK == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.PairMarkActive(*peerPK)
}

// PairSend publishes one message into the pair feed.
func (r *RPC) PairSend(req *PairSendRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "PairSend", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.PairSend(req.PeerPK, req.Text)
}

// PairPoll drains inbound pair messages with TS strictly after Since.
func (r *RPC) PairPoll(req *PairPollRequest, out *[]PairMessage) (err error) {
	defer rpcutil.LogCall(r.log, "PairPoll", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	msgs, err := r.visor.PairPoll(req.Since)
	if err != nil {
		return err
	}
	*out = msgs
	return nil
}
