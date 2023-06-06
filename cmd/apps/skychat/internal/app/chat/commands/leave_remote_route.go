// Package commands contains commands to leave remote room
package commands

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
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
	ms        messenger.Service
	visorRepo chat.Repository
	usrRepo   user.Repository
}

// NewLeaveRemoteRouteRequestHandler Handler constructor
func NewLeaveRemoteRouteRequestHandler(ms messenger.Service, visorRepo chat.Repository, usrRepo user.Repository) LeaveRemoteRouteRequestHandler {
	return leaveRemoteRouteRequestHandler{ms: ms, visorRepo: visorRepo, usrRepo: usrRepo}
}

// Handle Handles the LeaveRemoteRouteRequest request
func (h leaveRemoteRouteRequestHandler) Handle(command LeaveRemoteRouteRequest) error {
	usr, err := h.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	// Make sure that we don't leave a room of our own server
	if command.Route.Visor == usr.GetInfo().GetPK() {
		return fmt.Errorf("cannot leave route of own server")
	}

	// Check if visor exists
	visor, err := h.visorRepo.GetByPK(command.Route.Visor)
	if err != nil {
		return err
	}

	// Check if handling p2p room
	if command.Route.Server == command.Route.Room {
		if !visor.P2PIsEmpty() {
			err = h.ms.SendLeaveRouteMessage(command.Route)
			if err != nil {
				return err
			}
			err = visor.DeleteP2P()
			if err != nil {
				return err
			}
		}

		// Check if visor has servers, if not delete visor
		if len(visor.GetAllServer()) == 0 {
			return h.visorRepo.Delete(command.Route.Visor)
		}

		return h.visorRepo.Set(*visor)
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

	// Send LeaveChatMessage to remote server
	err = h.ms.SendLeaveRouteMessage(command.Route)
	if err != nil {
		fmt.Println(err)
	}

	// Delete room from server
	err = server.DeleteRoom(command.Route.Room)
	if err != nil {
		return err
	}

	//check if this was the last room of the server
	if len(server.GetAllRooms()) == 0 {
		//Prepare ServerRoute
		serverroute := util.NewServerRoute(command.Route.Server, command.Route.Server)
		// Send LeaveChatMessage to remote server
		err = h.ms.SendLeaveRouteMessage(serverroute)
		if err != nil {
			return err
		}
		err = visor.DeleteServer(command.Route.Server)
		if err != nil {
			return err
		}
	}

	// Check if visor has any other servers or p2p, if not delete visor
	if len(visor.GetAllServer()) == 0 && visor.P2PIsEmpty() {
		return h.visorRepo.Delete(command.Route.Visor)
	}

	// Update repository with changed visor
	return h.visorRepo.Set(*visor)
}
