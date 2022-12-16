// Package commands contains commands to add a remote route (this can be a visor, a server, or a room)
package commands

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// AddRemoteRouteRequest of AddRemoteRouteRequestHandler
type AddRemoteRouteRequest struct {
	Route util.PKRoute
}

// AddRemoteRouteRequestHandler struct that allows handling AddRemoteRouteRequest
type AddRemoteRouteRequestHandler interface {
	Handle(command AddRemoteRouteRequest) error
}

type addRemoteRouteRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewAddRemoteRouteRequestHandler Initializes an AddCommandHandler
func NewAddRemoteRouteRequestHandler(visorRepo chat.Repository, messengerService messenger.Service) AddRemoteRouteRequestHandler {
	return addRemoteRouteRequestHandler{visorRepo: visorRepo, messengerService: messengerService}
}

// Handle Handles the AddRemoteRouteRequest
func (h addRemoteRouteRequestHandler) Handle(command AddRemoteRouteRequest) error {
	fmt.Println("AddRemoteRouteHandler - Request: " + command.Route.String())
	//1. check if the requested route is already in visor repo
	visor, err := h.visorRepo.GetByPK(command.Route.Visor)
	if err == nil {
		server, err := visor.GetServerByPK(command.Route.Server)
		if err == nil {
			_, err := server.GetRoomByPK(command.Route.Room)
			if err == nil {
				return fmt.Errorf("room %s already added", command.Route.String())
			}
		}

	}

	err = h.messengerService.SendRouteRequestMessage(command.Route)
	if err != nil {
		return err
	}

	go h.messengerService.Handle(command.Route.Visor) //nolint:errcheck

	return nil
}
