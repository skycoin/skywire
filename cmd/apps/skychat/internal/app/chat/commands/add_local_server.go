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
	Info info.Info
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

	// Check if local visor exists, if not add the default local visor
	var visor chat.Visor
	pVisor, err := h.visorRepo.GetByPK(usr.GetInfo().GetPK())
	if err != nil {
		visor = chat.NewUndefinedVisor(usr.GetInfo().GetPK())
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

	//setup user as peer for memberships
	p := peer.NewPeer(*usr.GetInfo(), usr.GetInfo().Alias)

	//Add user as member from server
	err = server.AddMember(*p)
	if err != nil {
		return err
	}
	//Add user as admin, otherwise we can't send admin command messages to our own server
	err = server.AddAdmin(p.GetPK())
	if err != nil {
		return err
	}
	//Add user as member from room
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