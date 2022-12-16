// Package commands contains commands to add a local room
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// AddLocalRoomRequest of AddLocalRoomRequestHandler
type AddLocalRoomRequest struct {
	Route util.PKRoute
	Info  info.Info
	Type  int64
}

// AddLocalRoomRequestHandler struct that allows handling AddLocalRoomRequest
type AddLocalRoomRequestHandler interface {
	Handle(command AddLocalRoomRequest) error
}

type addLocalRoomRequestHandler struct {
	visorRepo chat.Repository
	userRepo  user.Repository
	ns        notification.Service
}

// NewAddLocalRoomRequestHandler Initializes an AddCommandHandler
func NewAddLocalRoomRequestHandler(visorRepo chat.Repository, userRepo user.Repository, ns notification.Service) AddLocalRoomRequestHandler {
	return addLocalRoomRequestHandler{visorRepo: visorRepo, userRepo: userRepo, ns: ns}
}

// Handle Handles the AddLocalRoomRequest
func (h addLocalRoomRequestHandler) Handle(command AddLocalRoomRequest) error {
	//Get user
	usr, err := h.userRepo.GetUser()
	if err != nil {
		return err
	}

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

	// make a new route
	rr := util.NewLocalRoomRoute(command.Route.Visor, command.Route.Server, server.GetAllRoomsBoolMap())

	// setup room for repository
	r := chat.NewLocalRoom(rr, command.Info, command.Type)

	//setup user as peer for room membership
	p := peer.NewPeer(*usr.GetInfo(), usr.GetInfo().Alias)
	//Add user as member
	err = r.AddMember(*p)
	if err != nil {
		return err
	}

	//[]: if room is visible/public also add messengerService and send 'Room-Added' Message to Members of server

	// add room to server, update visor and then update repository
	err = server.AddRoom(r)
	if err != nil {
		return err
	}
	err = visor.SetServer(*server)
	if err != nil {
		return err
	}

	err = h.visorRepo.Set(*visor)
	if err != nil {
		return err
	}

	//notify about sent chat request message
	n := notification.NewAddRouteNotification(rr)
	err = h.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}
