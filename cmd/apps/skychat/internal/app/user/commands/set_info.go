package commands

import (
	"errors"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

//SetInfoRequest of SetInfoRequestHandler
type SetInfoRequest struct {
	Alias string
	Desc  string
	Img   string
}

//SetInfoRequestHandler struct that allows handling SetInfoRequest
type SetInfoRequestHandler interface {
	Handle(command SetInfoRequest) error
}

type setInfoRequestHandler struct {
	usrRepo user.Repository
}

//NewSetInfoRequestHandler Initializes an SetInfoHandler
func NewSetInfoRequestHandler(usrRepo user.Repository) SetInfoRequestHandler {
	return setInfoRequestHandler{usrRepo: usrRepo}
}

//Handle Handles the SetInfoRequest
func (h setInfoRequestHandler) Handle(req SetInfoRequest) error {

	pUsr, err := h.usrRepo.GetUser()
	if err != nil {
		return errors.New("failed to get user")
	}

	i := info.NewInfo(pUsr.GetInfo().GetPK(), req.Alias, req.Desc, req.Img)

	pUsr.SetInfo(i)

	//TODO:Send info to peers that the info was updated

	return h.usrRepo.SetUser(pUsr)
}
