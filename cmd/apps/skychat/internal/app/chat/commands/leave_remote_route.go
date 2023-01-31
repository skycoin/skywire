// Package commands contains commands to leave remote room
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// LeaveRemoteRouteRequest Command Model
type LeaveRemoteRouteRequest struct {
	Route util.PKRoute
}

// LeaveRemoteRouteRequestHandler Handler Struct with Dependencies
type LeaveRemoteRouteRequestHandler interface {
	Handle(command LeaveRemoteRouteRequest) error
}

type leaveRemoteRouteRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewLeaveRemoteRouteRequestHandler Handler constructor
func NewLeaveRemoteRouteRequestHandler(messengerService messenger.Service, visorRepo chat.Repository) LeaveRemoteRouteRequestHandler {
	return leaveRemoteRouteRequestHandler{messengerService: messengerService, visorRepo: visorRepo}
}

// Handle Handles the LeaveRemoteRouteRequest request
func (h leaveRemoteRouteRequestHandler) Handle(command LeaveRemoteRouteRequest) error {
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

	//leave room if the request server and room are the same
	if command.Route.Server == command.Route.Room {
		// Check if room exists
		_, err = server.GetRoomByPK(command.Route.Room)
		if err != nil {
			return err
		}

		//TODO: Send Leave-Room-Message

		// Delete room from server
		err = server.DeleteRoom(command.Route.Room)
		if err != nil {
			return err
		}

		// Update visor with changed server
		err = visor.SetServer(*server)
		if err != nil {
			return err
		}

		//TODO:check if this was the last room of the server, if so maybe also leave server

		// Update repository with changed visor
		return h.visorRepo.Set(*visor)
	} else {

		//TODO: Send Leave-Server-Message

		// Delete server from visor
		err = visor.DeleteServer(command.Route.Server)
		if err != nil {
			return err
		}

		// Update repository with changed visor
		return h.visorRepo.Set(*visor)
	}

}
