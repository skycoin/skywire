// Package commands contains commands to delete peers of a user
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// DeletePeerRequest of DeletePeerRequestHandler
type DeletePeerRequest struct {
	PK cipher.PubKey
}

// DeletePeerRequestHandler struct that allows handling DeletePeerRequest
type DeletePeerRequestHandler interface {
	Handle(command DeletePeerRequest) error
}

type deletePeerRequestHandler struct {
	usrRepo user.Repository
}

// NewDeletePeerRequestHandler Initializes an DeletePeerRequestHandler
func NewDeletePeerRequestHandler(usrRepo user.Repository) DeletePeerRequestHandler {
	return deletePeerRequestHandler{usrRepo: usrRepo}
}

// Handle Handles the DeletePeerRequest
func (h deletePeerRequestHandler) Handle(req DeletePeerRequest) error {

	pUsr, err := h.usrRepo.GetUser()
	if err != nil {
		//TODO:(ersonp) check if something else needs to be done/closed on returning an error
		return err
	}

	pUsr.GetPeerbook().DeletePeer(req.PK)

	return h.usrRepo.SetUser(pUsr)
}
