package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// DeleteLocalRouteRequest of DeleteLocalRouteRequestHandler
type DeleteLocalRouteRequest struct {
	Route util.PKRoute
}

// DeleteLocalRouteRequestHandler struct that allows handling DeleteLocalRouteRequest
type DeleteLocalRouteRequestHandler interface {
	Handle(command DeleteLocalRouteRequest) error
}

type deleteLocalRouteRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewDeleteLocalRouteRequestHandler Initializes an AddCommandHandler
func NewDeleteLocalRouteRequestHandler(messengerService messenger.Service, visorRepo chat.Repository) DeleteLocalRouteRequestHandler {
	return deleteLocalRouteRequestHandler{messengerService: messengerService, visorRepo: visorRepo}
}

// Handle Handles the DeleteLocalRouteRequest
func (h deleteLocalRouteRequestHandler) Handle(command DeleteLocalRouteRequest) error {
	// Check if visor exists
	visor, err := h.visorRepo.GetByPK(command.Route.Visor)
	if err != nil {
		return err
	}
	// Check if server exists
	server, err := visor.GetServerByPK(command.Route.Server)
	if err != nil {
		return err
	}

	// if this, then we want to delete the whole server
	if command.Route.Server == command.Route.Room {
		//TODO: Send Room-Delete-Message to Room members in every room?
		//TODO: Send Server-Delete-Message to Server members
		err = visor.DeleteServer(command.Route.Server)
		if err != nil {
			return err
		}
		return h.visorRepo.Set(*visor)
	}

	// Check if room exists
	_, err = server.GetRoomByPK(command.Route.Room)
	if err != nil {
		return err
	}

	//TODO: Send Room-Delete-Message to Room members (and to server members, if room is public)
	err = server.DeleteRoom(command.Route.Room)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}
