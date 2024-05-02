// Package commands contains commands to delete local route
package commands

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/connectionhandler"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// DeleteRouteRequest of DeleteRouteRequestHandler
type DeleteRouteRequest struct {
	Route util.PKRoute
}

// DeleteRouteRequestHandler struct that allows handling DeleteRouteRequest
type DeleteRouteRequestHandler interface {
	Handle(command DeleteRouteRequest) error
}

type deleteRouteRequestHandler struct {
	connectionhandlerService connectionhandler.Service
	messengerService         messenger.Service
	visorRepo                chat.Repository
	usrRepo                  user.Repository
}

// NewDeleteRouteRequestHandler Initializes an AddCommandHandler
func NewDeleteRouteRequestHandler(connectionhandlerService connectionhandler.Service, messengerService messenger.Service, visorRepo chat.Repository, usrRepo user.Repository) DeleteRouteRequestHandler {
	return deleteRouteRequestHandler{
		connectionhandlerService: connectionhandlerService,
		messengerService:         messengerService,
		visorRepo:                visorRepo,
		usrRepo:                  usrRepo}
}

// Handle Handles the DeleteRouteRequest
func (h deleteRouteRequestHandler) Handle(command DeleteRouteRequest) error {
	if h.routeIsOfOwnVisor(command.Route) {
		err := h.deleteLocalRoute(command)
		if err != nil {
			return err
		}
	}

	err := h.deleteRemoteRoute(command)
	if err != nil {
		return err
	}

	return nil
}

func (h deleteRouteRequestHandler) routeIsOfOwnVisor(route util.PKRoute) bool {
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

func (h deleteRouteRequestHandler) deleteLocalRoute(command DeleteRouteRequest) error {
	if command.isDeleteServerRouteCommand() {
		err := h.deleteServerRoute(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteVisorIfEmpty(command.Route)
		if err != nil {
			return err
		}
		return nil
	}

	if command.isDeleteRoomRouteCommand() {
		err := h.deleteRoomRoute(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteServerIfEmpty(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteVisorIfEmpty(command.Route)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h deleteRouteRequestHandler) deleteRemoteRoute(command DeleteRouteRequest) error {
	if command.isDeleteP2PRouteCommand() {
		err := h.deleteP2PRoute(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteVisorIfEmpty(command.Route)
		if err != nil {
			return err
		}
		return nil
	}

	if command.isDeleteServerRouteCommand() {
		err := h.deleteServerRoute(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteVisorIfEmpty(command.Route)
		if err != nil {
			return err
		}
		return nil
	}

	if command.isDeleteRoomRouteCommand() {
		err := h.deleteRoomRoute(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteServerIfEmpty(command.Route)
		if err != nil {
			return err
		}
		err = h.deleteVisorIfEmpty(command.Route)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c DeleteRouteRequest) isDeleteP2PRouteCommand() bool {
	return c.Route.IsP2PRoute()
}

func (c DeleteRouteRequest) isDeleteServerRouteCommand() bool {
	return c.Route.IsServerRoute()
}

func (c DeleteRouteRequest) isDeleteRoomRouteCommand() bool {
	return c.Route.IsRoomRoute()
}

func (h deleteRouteRequestHandler) deleteVisorIfEmpty(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	if len(visor.GetAllServer()) == 0 && visor.P2PIsEmpty() {
		err = h.connectionhandlerService.UnhandleConnection(route.Visor)
		if err != nil {
			return err
		}
		return h.visorRepo.Delete(route.Visor)
	}

	return nil
}

func (h deleteRouteRequestHandler) deleteServerIfEmpty(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	server, err := visor.GetServerByPK(route.Server)
	if err != nil {
		return err
	}

	if len(server.GetAllRooms()) == 0 {
		return h.deleteServerRoute(route)
	}

	return nil
}

func (h deleteRouteRequestHandler) deleteP2PRoute(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	err = visor.DeleteP2P()
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}

func (h deleteRouteRequestHandler) deleteServerRoute(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	err = visor.DeleteServer(route.Server)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}

func (h deleteRouteRequestHandler) deleteRoomRoute(route util.PKRoute) error {
	visor, err := h.visorRepo.GetByPK(route.Visor)
	if err != nil {
		return err
	}

	server, err := visor.GetServerByPK(route.Server)
	if err != nil {
		return err
	}

	err = server.DeleteRoom(route.Room)
	if err != nil {
		return err
	}

	err = visor.SetServer(*server)
	if err != nil {
		return err
	}

	return h.visorRepo.Set(*visor)
}
