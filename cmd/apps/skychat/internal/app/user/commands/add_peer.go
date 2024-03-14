// Package commands contains commands to add peers of a user
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// AddPeerRequest of AddPeerRequestHandler
type AddPeerRequest struct {
	Info  info.Info
	Alias string
}

// AddPeerRequestHandler struct that allows handling AddPeerRequest
type AddPeerRequestHandler interface {
	Handle(command AddPeerRequest) error
}

type addPeerRequestHandler struct {
	usrRepo user.Repository
}

// NewAddPeerRequestHandler Initializes an AddPeerRequestHandler
func NewAddPeerRequestHandler(usrRepo user.Repository) AddPeerRequestHandler {
	return addPeerRequestHandler{usrRepo: usrRepo}
}

// Handle Handles the AddPeerRequest
func (h addPeerRequestHandler) Handle(req AddPeerRequest) error {

	pUsr, err := h.usrRepo.GetUser()
	if err != nil {
		return err
		//TODO:(ersonp) check if something else needs to be done/closed on returning an error
	}

	pUsr.GetPeerbook().AddPeer(req.Info, req.Alias)

	return h.usrRepo.SetUser(pUsr)
}
