// Package commands contains commands to leave remote room
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// LeaveRemoteRoomRequest Command Model
type LeaveRemoteRoomRequest struct {
	Route util.PKRoute
}

// LeaveRemoteRoomRequestHandler Handler Struct with Dependencies
type LeaveRemoteRoomRequestHandler interface {
	Handle(command LeaveRemoteRoomRequest) error
}

type leaveRemoteRoomRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewLeaveRemoteRoomRequestHandler Handler constructor
func NewLeaveRemoteRoomRequestHandler(messengerService messenger.Service, visorRepo chat.Repository) LeaveRemoteRoomRequestHandler {
	return leaveRemoteRoomRequestHandler{messengerService: messengerService, visorRepo: visorRepo}
}

// Handle Handles the LeaveRemoteRoomRequest request
func (h leaveRemoteRoomRequestHandler) Handle(command LeaveRemoteRoomRequest) error {
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

	//[]: send 'Leave-Room-Message' to remote server so membership of room can be removed
	//?: this command is nearly the same as delete_local_room_by_route.go -> maybe remove one and add this part here with if visorpk=localvisor
	//! -> When the 'Leave-Room-Message' does not get recognized from the room, all 'Room-Messages' still get send -> How to solve?

	//?if this is the last Room maybe also leave server? -> and do everything that is done when a server is left

	// Delete room from server
	err = server.DeleteRoom(command.Route.Room)
	if err != nil {
		return err
	}
	// Update visor, and repository with changed server
	err = visor.SetServer(*server)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}
