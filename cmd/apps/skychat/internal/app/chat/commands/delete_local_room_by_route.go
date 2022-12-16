package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// DeleteLocalRoomRequest of DeleteLocalRoomRequestHandler
type DeleteLocalRoomRequest struct {
	Route util.PKRoute
}

// DeleteLocalRoomRequestHandler struct that allows handling DeleteLocalRoomRequest
type DeleteLocalRoomRequestHandler interface {
	Handle(command DeleteLocalRoomRequest) error
}

type deleteLocalRoomRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewDeleteLocalRoomRequestHandler Initializes an AddCommandHandler
func NewDeleteLocalRoomRequestHandler(messengerService messenger.Service, visorRepo chat.Repository) DeleteLocalRoomRequestHandler {
	return deleteLocalRoomRequestHandler{messengerService: messengerService, visorRepo: visorRepo}
}

// Handle Handles the DeleteLocalRoomRequest
func (h deleteLocalRoomRequestHandler) Handle(command DeleteLocalRoomRequest) error {
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
	// Check if room exists
	_, err = server.GetRoomByPK(command.Route.Room)
	if err != nil {
		return err
	}

	// Delete room from server
	err = server.DeleteRoom(command.Route.Room)
	if err != nil {
		return err
	}

	//[]: Send 'Room-Deleted-Message' to room members (and to server members if room is public)

	// Update visor, and repository with changed server
	err = visor.SetServer(*server)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}
