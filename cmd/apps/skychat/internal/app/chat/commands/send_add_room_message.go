// Package commands contains commands to send add room message
package commands

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendAddRoomMessageRequest of SendAddRoomMessageRequestHandler
type SendAddRoomMessageRequest struct {
	Route util.PKRoute
	Info  info.Info
	Type  int
}

// SendAddRoomMessageRequestHandler struct that allows handling SendAddRoomMessageRequest
type SendAddRoomMessageRequestHandler interface {
	Handle(command SendAddRoomMessageRequest) error
}

type sendAddRoomMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendAddRoomMessageRequestHandler Initializes an AddCommandHandler
func NewSendAddRoomMessageRequestHandler(messengerService messenger.Service) SendAddRoomMessageRequestHandler {
	return sendAddRoomMessageRequestHandler{messengerService: messengerService}
}

// Handle handles the SendAddRoomMessageRequest
func (h sendAddRoomMessageRequestHandler) Handle(command SendAddRoomMessageRequest) error {
	err := h.messengerService.SendAddRoomMessage(command.Route, command.Info)
	if err != nil {
		return err
	}

	return nil
}
