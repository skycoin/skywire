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
	if h.routeIsOfOwnVisor(command.Route) {
		return fmt.Errorf("cannot leave route of own server")
	}

	if command.isLeavingP2PRouteCommand() {
		err := h.leaveP2PRoute(command.Route)
		if err != nil {
			return err
		}
		return nil
	}

	if command.isLeavingServerRouteCommand() {
		err := h.leaveServerRoute(command.Route)
		if err != nil {
			return err
		}
		return nil
	}

	if command.isLeavingRoomRouteCommand() {
		err := h.leaveRoomRoute(command.Route)
		if err != nil {
			return err
		}

		err = h.leaveServerIfEmpty(command.Route)
		if err != nil {
			return err
		}

		return nil
	}
	return nil
}

func (h leaveRemoteRouteRequestHandler) routeIsOfOwnVisor(route util.PKRoute) bool {
	usr, err := h.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		//return err
		//TODO: handle different?
		return true
	}

	if route.Visor == usr.GetInfo().GetPK() {
		return true
	}

	return false
}

func (c LeaveRemoteRouteRequest) isLeavingP2PRouteCommand() bool {
	return c.Route.IsP2PRoute()
}

func (c LeaveRemoteRouteRequest) isLeavingServerRouteCommand() bool {
	return c.Route.IsServerRoute()
}

func (c LeaveRemoteRouteRequest) isLeavingRoomRouteCommand() bool {
	return c.Route.IsRoomRoute()
}

func (h leaveRemoteRouteRequestHandler) leaveP2PRoute(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	if !visor.P2PIsEmpty() {
		err = h.ms.SendLeaveRouteMessage(route)
		if err != nil {
			return err
		}
	}

	return h.visorRepo.Set(*visor)
}

func (h leaveRemoteRouteRequestHandler) leaveServerRoute(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	_, err = visor.GetServerByPK(route.Server)
	if err != nil {
		return err
	}

	err = h.ms.SendLeaveRouteMessage(route)
	if err != nil {
		fmt.Println(err)
	}

	return h.visorRepo.Set(*visor)
}

func (h leaveRemoteRouteRequestHandler) leaveRoomRoute(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	server, err := visor.GetServerByPK(route.Server)
	if err != nil {
		return err
	}

	_, err = server.GetRoomByPK(route.Room)
	if err != nil {
		return err
	}

	err = h.ms.SendLeaveRouteMessage(route)
	if err != nil {
		fmt.Println(err)
	}

	return nil
}

func (h leaveRemoteRouteRequestHandler) leaveServerIfEmpty(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	server, err := visor.GetServerByPK(route.Server)
	if err != nil {
		return err
	}

	if len(server.GetAllRooms()) == 0 {
		//Prepare ServerRoute
		serverroute := util.NewServerRoute(route.Visor, route.Server)
		// Send LeaveChatMessage to remote server
		err = h.ms.SendLeaveRouteMessage(serverroute)
		if err != nil {
			return err
		}

		return nil
	}

	return nil
}
