package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// DeleteLocalServerRequest of DeleteLocalServerRequestHandler
type DeleteLocalServerRequest struct {
	Route util.PKRoute
}

// DeleteLocalServerRequestHandler struct that allows handling DeleteLocalServerRequest
type DeleteLocalServerRequestHandler interface {
	Handle(command DeleteLocalServerRequest) error
}

type deleteLocalServerRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewDeleteLocalServerRequestHandler Initializes an AddCommandHandler
func NewDeleteLocalServerRequestHandler(messengerService messenger.Service, visorRepo chat.Repository) DeleteLocalServerRequestHandler {
	return deleteLocalServerRequestHandler{messengerService: messengerService, visorRepo: visorRepo}
}

// Handle Handles the DeleteLocalServerRequest
func (h deleteLocalServerRequestHandler) Handle(command DeleteLocalServerRequest) error {
	// Check if visor exists
	visor, err := h.visorRepo.GetByPK(command.Route.Visor)
	if err != nil {
		return err
	}
	// Check if server exists
	_, err = visor.GetServerByPK(command.Route.Server)
	if err != nil {
		return err
	}

	//[]: Send 'Server-Deleted-Message' to Server members

	// Delete server from visor and update repo
	err = visor.DeleteServer(command.Route.Server)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}
