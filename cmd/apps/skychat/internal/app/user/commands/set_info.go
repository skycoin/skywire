// Package commands contains commands to set info of a user
package commands

import (
	"errors"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SetInfoRequest of SetInfoRequestHandler
type SetInfoRequest struct {
	Alias string
	Desc  string
	Img   string
}

// SetInfoRequestHandler struct that allows handling SetInfoRequest
type SetInfoRequestHandler interface {
	Handle(command SetInfoRequest) error
}

type setInfoRequestHandler struct {
	ms        messenger.Service
	usrRepo   user.Repository
	visorRepo chat.Repository
}

// NewSetInfoRequestHandler Initializes an SetInfoHandler
func NewSetInfoRequestHandler(ms messenger.Service, usrRepo user.Repository, visorRepo chat.Repository) SetInfoRequestHandler {
	return setInfoRequestHandler{ms: ms, usrRepo: usrRepo, visorRepo: visorRepo}
}

// Handle Handles the SetInfoRequest
func (h setInfoRequestHandler) Handle(req SetInfoRequest) error {

	pUsr, err := h.usrRepo.GetUser()
	if err != nil {
		return errors.New("failed to get user")
	}

	i := info.NewInfo(pUsr.GetInfo().GetPK(), req.Alias, req.Desc, req.Img)

	pUsr.SetInfo(i)

	//get all visors
	visors, err := h.visorRepo.GetAll()
	if err != nil {
		return err
	}

	//TODO: send info only to visors and visor then handles the info message to update all of its servers etc. where originator is member of (much less messages to send)
	//send info message to each pkroute we know
	for _, visor := range visors {

		//send the users info to p2p if it is not empty
		if !visor.P2PIsEmpty() {
			root := util.NewP2PRoute(pUsr.GetInfo().Pk)

			p2p, err := visor.GetP2P()
			if err != nil {
				return err
			}

			dest := p2p.GetPKRoute()

			err = h.ms.SendInfoMessage(dest, root, dest, *pUsr.GetInfo())
			if err != nil {
				return err
			}
		}

		servers := visor.GetAllServer()

		for _, server := range servers {

			rooms := server.GetAllRooms()

			for _, room := range rooms {
				//send the users info to the remote or local route
				root := util.NewP2PRoute(pUsr.GetInfo().Pk)
				dest := room.PKRoute

				err = h.ms.SendInfoMessage(room.PKRoute, root, dest, *pUsr.GetInfo())
				if err != nil {
					return err
				}
			}

		}
	}
	return h.usrRepo.SetUser(pUsr)
}
