// Package commands containotifService commands to add a local server
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
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
	visorRepo    chat.Repository
	userRepo     user.Repository
	notifService notification.Service
}

// NewAddLocalServerRequestHandler Initializes an AddCommandHandler
func NewAddLocalServerRequestHandler(visorRepo chat.Repository, userRepo user.Repository, notifService notification.Service) AddLocalServerRequestHandler {
	return addLocalServerRequestHandler{visorRepo: visorRepo, userRepo: userRepo, notifService: notifService}
}

// Handle Handles the AddLocalServerRequest
func (h addLocalServerRequestHandler) Handle(command AddLocalServerRequest) error {

	visor, err := h.getLocalVisorOrAddIfNotExists()
	if err != nil {
		return err
	}

	server, err := h.getNewServer(visor, command)
	if err != nil {
		return err
	}

	room, err := h.getNewRoom(server, command)
	if err != nil {
		return err
	}

	err = server.AddRoom(*room)
	if err != nil {
		return err
	}

	err = visor.AddServer(*server)
	if err != nil {
		return err
	}
	err = h.visorRepo.Set(*visor)
	if err != nil {
		return err
	}

	n := notification.NewAddRouteNotification(room.GetPKRoute())
	err = h.notifService.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

func (h addLocalServerRequestHandler) getLocalVisorOrAddIfNotExists() (*chat.Visor, error) {
	userPK, err := h.getUserPK()
	if err != nil {
		return nil, err
	}

	visorIfExists, err := h.visorRepo.GetByPK(*userPK)
	if err != nil {
		newVisor := chat.NewUndefinedVisor(*userPK)
		err = h.visorRepo.Add(newVisor)
		if err != nil {
			return nil, err
		}
		return &newVisor, nil
	}
	return visorIfExists, nil
}

func (h addLocalServerRequestHandler) getUserPK() (*cipher.PubKey, error) {
	usr, err := h.userRepo.GetUser()
	if err != nil {
		return nil, err
	}

	pk := usr.GetInfo().GetPK()
	return &pk, nil
}

func (h addLocalServerRequestHandler) getNewServer(visor *chat.Visor, command AddLocalServerRequest) (*chat.Server, error) {
	userAsPeer, err := h.getUserAsPeer()
	if err != nil {
		return nil, err
	}

	visorBoolMap := visor.GetAllServerBoolMap()

	route := util.NewLocalServerRoute(visor.GetPK(), visorBoolMap)
	server, err := chat.NewLocalServer(route, command.Info)
	if err != nil {
		return nil, err
	}

	//Add user as member, otherwise we can't receive server messages //?? check if really needed.
	err = server.AddMember(*userAsPeer)
	if err != nil {
		return nil, err
	}

	//Add user as admin, otherwise we can't send admin command messages to our own server
	err = server.AddAdmin(userAsPeer.GetPK())
	if err != nil {
		return nil, err
	}
	return server, nil
}

func (h addLocalServerRequestHandler) getNewRoom(server *chat.Server, command AddLocalServerRequest) (*chat.Room, error) {

	roomBoolMap := server.GetAllRoomsBoolMap()
	roomRoute := util.NewLocalRoomRoute(server.PKRoute.Visor, server.PKRoute.Server, roomBoolMap)
	room := chat.NewLocalRoom(roomRoute, command.Info, chat.DefaultRoomType)

	//Add user as member, otherwise we can't receive room messages //?? check if really needed.
	userAsPeer, err := h.getUserAsPeer()
	if err != nil {
		return nil, err
	}
	err = room.AddMember(*userAsPeer)
	if err != nil {
		return nil, err
	}
	return &room, nil
}

func (h addLocalServerRequestHandler) getUserAsPeer() (*peer.Peer, error) {
	usr, err := h.userRepo.GetUser()
	if err != nil {
		return nil, err
	}
	p := peer.NewPeer(*usr.GetInfo(), usr.GetInfo().Alias)
	return p, nil
}
