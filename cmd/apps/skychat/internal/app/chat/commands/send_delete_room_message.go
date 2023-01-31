// Package commands contains commands to send text message
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendDeleteRoomMessageRequest of SendDeleteRoomMessageRequestHandler
type SendDeleteRoomMessageRequest struct {
	Route util.PKRoute
}

// SendDeleteRoomMessageRequestHandler struct that allows handling SendDeleteRoomMessageRequest
type SendDeleteRoomMessageRequestHandler interface {
	Handle(command SendDeleteRoomMessageRequest) error
}

type sendDeleteRoomMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendDeleteRoomMessageRequestHandler Initializes an AddCommandHandler
func NewSendDeleteRoomMessageRequestHandler(messengerService messenger.Service) SendDeleteRoomMessageRequestHandler {
	return sendDeleteRoomMessageRequestHandler{messengerService: messengerService}
}

// Handle Handles the AddCragRequest
func (h sendDeleteRoomMessageRequestHandler) Handle(command SendDeleteRoomMessageRequest) error {
	err := h.messengerService.SendDeleteRoomMessage(command.Route)
	if err != nil {
		return err
	}

	return nil
}
