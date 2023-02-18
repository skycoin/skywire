// Package commands contains commands to send text message
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendMutePeerMessageRequest of SendMutePeerMessageRequestHandler
type SendMutePeerMessageRequest struct {
	Route util.PKRoute
	Pk    cipher.PubKey
}

// SendMutePeerMessageRequestHandler struct that allows handling SendMutePeerMessageRequest
type SendMutePeerMessageRequestHandler interface {
	Handle(command SendMutePeerMessageRequest) error
}

type sendMutePeerMessageRequestHandler struct {
	messengerService messenger.Service
}

// NewSendMutePeerMessageRequestHandler Initializes an AddCommandHandler
func NewSendMutePeerMessageRequestHandler(messengerService messenger.Service) SendMutePeerMessageRequestHandler {
	return sendMutePeerMessageRequestHandler{messengerService: messengerService}
}

// Handle handles the SendMutePeerMessageRequest
func (h sendMutePeerMessageRequestHandler) Handle(command SendMutePeerMessageRequest) error {
	err := h.messengerService.SendMutePeerMessage(command.Route, command.Pk)
	if err != nil {
		return err
	}

	return nil
}
