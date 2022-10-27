package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/settings"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

//SetSettingsRequest of SetSettingsRequestHandler
type SetSettingsRequest struct {
	Blacklist []cipher.PubKey
}

//SetSettingsRequestHandler struct that allows handling SetSettingsRequest
type SetSettingsRequestHandler interface {
	Handle(command SetSettingsRequest) error
}

type setSettingsRequestHandler struct {
	usrRepo user.Repository
}

//NewSetSettingsRequestHandler Initializes an SetSettingsRequestHandler
func NewSetSettingsRequestHandler(usrRepo user.Repository) SetSettingsRequestHandler {
	return setSettingsRequestHandler{usrRepo: usrRepo}
}

//Handle Handles the SetSettingsRequest
func (h setSettingsRequestHandler) Handle(req SetSettingsRequest) error {

	pUsr, err := h.usrRepo.GetUser()
	if err != nil {
		//TODO: implement error
		//TODO:(ersonp) check if something else needs to be done/closed on returning an error
		return err
	}

	s := settings.NewSettings(req.Blacklist)

	pUsr.SetSettings(s)

	return h.usrRepo.SetUser(pUsr)
}
