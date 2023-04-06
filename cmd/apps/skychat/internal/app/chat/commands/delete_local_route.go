// Package commands contains commands to delete local route
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
		//Send Server-Delete-Message to Server members -> they then have to delete or disable the function to send new messages to the server
		serverroute := util.NewServerRoute(command.Route.Visor, command.Route.Server)
		err = h.messengerService.SendRouteDeletedMessage(serverroute)
		if err != nil {
			return err
		}

		//Delete server from visor
		err = visor.DeleteServer(command.Route.Server)
		if err != nil {
			return err
		}

		//update visorrepository
		return h.visorRepo.Set(*visor)
	}

	// Check if room exists
	room, err := server.GetRoomByPK(command.Route.Room)
	if err != nil {
		return err
	}

	//Send Room-Deleted-Message to room members
	err = h.messengerService.SendRouteDeletedMessage(command.Route)
	if err != nil {
		return err
	}

	//if room is public/visibile also send message to server members
	if room.GetIsVisible() {
		serverroute := util.NewServerRoute(command.Route.Visor, command.Route.Server)
		err = h.messengerService.SendRouteDeletedMessage(serverroute)
		if err != nil {
			return err
		}
	}

	//delete room from server
	err = server.DeleteRoom(command.Route.Room)
	if err != nil {
		return err
	}

	//update visor
	err = visor.SetServer(*server)
	if err != nil {
		return err
	}

	//update visorrepository
	return h.visorRepo.Set(*visor)
}
