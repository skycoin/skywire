// Package commands contains commands to send unmute peer message
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendUnmutePeerMessageRequest of SendUnmutePeerMessageRequestHandler
type SendUnmutePeerMessageRequest struct {
	Route util.PKRoute
	Pk    cipher.PubKey
}

// SendUnmutePeerMessageRequestHandler struct that allows handling SendUnmutePeerMessageRequest
type SendUnmutePeerMessageRequestHandler interface {
	Handle(command SendUnmutePeerMessageRequest) error
}

type sendUnmutePeerMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendUnmutePeerMessageRequestHandler Initializes an AddCommandHandler
func NewSendUnmutePeerMessageRequestHandler(messengerService messenger.Service) SendUnmutePeerMessageRequestHandler {
	return sendUnmutePeerMessageRequestHandler{messengerService: messengerService}
}

// Handle handles the SendUnmutePeerMessageRequest
func (h sendUnmutePeerMessageRequestHandler) Handle(command SendUnmutePeerMessageRequest) error {
	err := h.messengerService.SendUnmutePeerMessage(command.Route, command.Pk)
	if err != nil {
		return err
	}

	return nil
}
