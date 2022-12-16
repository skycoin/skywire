// Package commands contains commands to send text message
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendTextMessageRequest of SendTextMessageRequestHandler
// To send a message as p2p message the route must fulfill: PubKey of visor = PubKey of server or only the visor PubKey is defined in route
// Else you have to define the complete route
type SendTextMessageRequest struct {
	Route util.PKRoute
	Msg   []byte
}

// SendTextMessageRequestHandler struct that allows handling SendTextMessageRequest
type SendTextMessageRequestHandler interface {
	Handle(command SendTextMessageRequest) error
}

type sendTextMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendTextMessageRequestHandler Initializes an AddCommandHandler
func NewSendTextMessageRequestHandler(messengerService messenger.Service) SendTextMessageRequestHandler {
	return sendTextMessageRequestHandler{messengerService: messengerService}
}

// Handle Handles the AddCragRequest
func (h sendTextMessageRequestHandler) Handle(command SendTextMessageRequest) error {
	err := h.messengerService.SendTextMessage(command.Route, command.Msg)
	if err != nil {
		return err
	}

	return nil
}
