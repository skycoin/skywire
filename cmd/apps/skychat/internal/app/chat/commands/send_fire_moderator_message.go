// Package commands contains commands to send fire moderator message
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendFireModeratorMessageRequest of SendFireModeratorMessageRequestHandler
type SendFireModeratorMessageRequest struct {
	Route util.PKRoute
	Pk    cipher.PubKey
}

// SendFireModeratorMessageRequestHandler struct that allows handling SendFireModeratorMessageRequest
type SendFireModeratorMessageRequestHandler interface {
	Handle(command SendFireModeratorMessageRequest) error
}

type sendFireModeratorMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendFireModeratorMessageRequestHandler Initializes an AddCommandHandler
func NewSendFireModeratorMessageRequestHandler(messengerService messenger.Service) SendFireModeratorMessageRequestHandler {
	return sendFireModeratorMessageRequestHandler{messengerService: messengerService}
}

// Handle handles the SendFireModeratorMessageRequest
func (h sendFireModeratorMessageRequestHandler) Handle(command SendFireModeratorMessageRequest) error {
	err := h.messengerService.SendFireModeratorMessage(command.Route, command.Pk)
	if err != nil {
		return err
	}

	return nil
}
