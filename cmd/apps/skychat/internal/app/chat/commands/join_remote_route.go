// Package commands contains commands to add a remote route (this can be a visor, a server, or a room)
package commands

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// JoinRemoteRouteRequest of JoinRemoteRouteRequestHandler
type JoinRemoteRouteRequest struct {
	Route util.PKRoute
}

// JoinRemoteRouteRequestHandler struct that allows handling JoinRemoteRouteRequest
type JoinRemoteRouteRequestHandler interface {
	Handle(command JoinRemoteRouteRequest) error
}

type joinRemoteRouteRequestHandler struct {
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewJoinRemoteRouteRequestHandler Initializes an JoinCommandHandler
func NewJoinRemoteRouteRequestHandler(visorRepo chat.Repository, messengerService messenger.Service) JoinRemoteRouteRequestHandler {
	return joinRemoteRouteRequestHandler{visorRepo: visorRepo, messengerService: messengerService}
}

// Handle Handles the JoinRemoteRouteRequest
func (h joinRemoteRouteRequestHandler) Handle(command JoinRemoteRouteRequest) error {
	if h.routeAlreadyJoined(command) {
		return fmt.Errorf("already member of route %s", command.Route.String())
	}

	err := h.messengerService.SendRouteRequestMessage(command.Route)
	if err != nil {
		return err
	}

	return nil
}

// routeAlreadyJoined returns if the user already is member of the route he wants to join
func (h joinRemoteRouteRequestHandler) routeAlreadyJoined(command JoinRemoteRouteRequest) bool {
	visor, err := h.visorRepo.GetByPK(command.Route.Visor)
	if err == nil {
		server, err := visor.GetServerByPK(command.Route.Server)
		if err == nil {
			_, err := server.GetRoomByPK(command.Route.Room)
			if err == nil {
				return true
			}
		}
	}
	return false
}
