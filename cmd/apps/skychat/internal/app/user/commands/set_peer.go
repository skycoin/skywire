// Package commands contains commands to set peers of a user
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// SetPeerRequest of SetPeerRequestHandler
type SetPeerRequest struct {
	Peer peer.Peer
}

// SetPeerRequestHandler struct that allows handling SetPeerRequest
type SetPeerRequestHandler interface {
	Handle(command SetPeerRequest) error
}

type setPeerRequestHandler struct {
	usrRepo user.Repository
}

// NewSetPeerRequestHandler Initializes an SetPeerRequestHandler
func NewSetPeerRequestHandler(usrRepo user.Repository) SetPeerRequestHandler {
	return setPeerRequestHandler{usrRepo: usrRepo}
}

// Handle Handles the SetPeerRequest
func (h setPeerRequestHandler) Handle(req SetPeerRequest) error {

	pUsr, err := h.usrRepo.GetUser()
	if err != nil {
		//TODO:(ersonp) check if something else needs to be done/closed on returning an error
		return err
	}

	pUsr.GetPeerbook().SetPeer(req.Peer)

	return h.usrRepo.SetUser(pUsr)
}
