// Package commands contains commands to add a local server
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// AddLocalServerRequest of AddLocalServerRequestHandler
type AddLocalServerRequest struct {
	Route util.PKRoute
	Info  info.Info
}

// AddLocalServerRequestHandler struct that allows handling AddLocalServerRequest
type AddLocalServerRequestHandler interface {
	Handle(command AddLocalServerRequest) error
}

type addLocalServerRequestHandler struct {
	visorRepo chat.Repository
	userRepo  user.Repository
	ns        notification.Service
}

// NewAddLocalServerRequestHandler Initializes an AddCommandHandler
func NewAddLocalServerRequestHandler(visorRepo chat.Repository, userRepo user.Repository, ns notification.Service) AddLocalServerRequestHandler {
	return addLocalServerRequestHandler{visorRepo: visorRepo, userRepo: userRepo, ns: ns}
}

// Handle Handles the AddLocalServerRequest
func (h addLocalServerRequestHandler) Handle(command AddLocalServerRequest) error {
	//Get user
	usr, err := h.userRepo.GetUser()
	if err != nil {
		return err
	}

	//TODO: Rewrite so the local visor pk must not be known from requester (-> use userRepo)
	// Check if local visor exists, if not add the default local visor
	visor := chat.Visor{}
	pVisor, err := h.visorRepo.GetByPK(command.Route.Visor)
	if err != nil {
		visor = chat.NewUndefinedVisor(command.Route.Visor)
		err = h.visorRepo.Add(visor)
		if err != nil {
			return err
		}
	} else {
		visor = *pVisor
	}

	visorBoolMap := visor.GetAllServerBoolMap()

	route := util.NewLocalServerRoute(visor.GetPK(), visorBoolMap)
	server, err := chat.NewLocalServer(route, command.Info)
	if err != nil {
		return err
	}

	//setup room
	roomBoolMap := server.GetAllRoomsBoolMap()
	roomRoute := util.NewLocalRoomRoute(server.PKRoute.Visor, server.PKRoute.Server, roomBoolMap)
	r := chat.NewLocalRoom(roomRoute, command.Info, chat.DefaultRoomType)

	//setup user as peer for room membership
	p := peer.NewPeer(*usr.GetInfo(), usr.GetInfo().Alias)
	//Add user as member
	err = r.AddMember(*p)
	if err != nil {
		return err
	}

	//Add room to server
	err = server.AddRoom(r)
	if err != nil {
		return err
	}

	// Add server to visor and then update repository
	err = visor.AddServer(*server)
	if err != nil {
		return err
	}
	err = h.visorRepo.Set(visor)
	if err != nil {
		return err
	}

	//notify about sent chat request message
	n := notification.NewAddRouteNotification(roomRoute)
	err = h.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}
