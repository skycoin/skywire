// Package commands contains commands to send hire moderator message
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendHireModeratorMessageRequest of SendHireModeratorMessageRequestHandler
type SendHireModeratorMessageRequest struct {
	Route util.PKRoute
	Pk    cipher.PubKey
}

// SendHireModeratorMessageRequestHandler struct that allows handling SendHireModeratorMessageRequest
type SendHireModeratorMessageRequestHandler interface {
	Handle(command SendHireModeratorMessageRequest) error
}

type sendHireModeratorMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendHireModeratorMessageRequestHandler Initializes an AddCommandHandler
func NewSendHireModeratorMessageRequestHandler(messengerService messenger.Service) SendHireModeratorMessageRequestHandler {
	return sendHireModeratorMessageRequestHandler{messengerService: messengerService}
}

// Handle handles the SendHireModeratorMessageRequest
func (h sendHireModeratorMessageRequestHandler) Handle(command SendHireModeratorMessageRequest) error {
	err := h.messengerService.SendHireModeratorMessage(command.Route, command.Pk)
	if err != nil {
		return err
	}

	return nil
}
