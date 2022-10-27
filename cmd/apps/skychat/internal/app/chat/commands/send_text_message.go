// Package commands contains commands to send text message
package commands

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
)

// SendTextMessageRequest of SendTextMessageRequestHandler
type SendTextMessageRequest struct {
	Pk  cipher.PubKey
	Msg []byte
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
func (h sendTextMessageRequestHandler) Handle(req SendTextMessageRequest) error {

	err := h.messengerService.SendTextMessage(req.Pk, req.Msg)
	if err != nil {
		return err
	}

	return nil
}
