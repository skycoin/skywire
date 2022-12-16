// Package commands contains commands to leave remote room
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// LeaveRemoteServerRequest Command Model
type LeaveRemoteServerRequest struct {
	Route util.PKRoute
}

// LeaveRemoteServerRequestHandler Handler Struct with Dependencies
type LeaveRemoteServerRequestHandler interface {
	Handle(command LeaveRemoteServerRequest) error
}

type leaveRemoteServerRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewLeaveRemoteServerRequestHandler Handler constructor
func NewLeaveRemoteServerRequestHandler(messengerService messenger.Service, visorRepo chat.Repository) LeaveRemoteServerRequestHandler {
	return leaveRemoteServerRequestHandler{messengerService: messengerService, visorRepo: visorRepo}
}

// Handle Handles the LeaveRemoteServerRequest request
func (h leaveRemoteServerRequestHandler) Handle(command LeaveRemoteServerRequest) error {
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

	//[]: Send 'Leave-Server-Message' to remote server so membership of it can be removed
	//? this command is nearly the same as delete_local_server_by_route.go -> maybe remove one and add this part here with if visorpk=localvisor
	//! -> When the 'Leave-Server-Message' does not get recognized from the server, all 'Server-Messages' still get send -> How to solve?

	// Delete server from visor
	err = visor.DeleteServer(command.Route.Server)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}
