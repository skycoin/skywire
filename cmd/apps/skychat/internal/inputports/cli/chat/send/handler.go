// Package clichatsend contains code of the cobra-cli handler for inputports
package clichatsend

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	chatservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Handler clichat request handler
type Handler struct {
	chatServices chatservices.ChatServices
}

// NewHandler Constructor returns *Handler
func NewHandler(cs chatservices.ChatServices) *Handler {
	return &Handler{chatServices: cs}
}

// SendTextMessageRequestModel represents the request model expected for send text request
type SendTextMessageRequestModel struct {
	VisorPk  cipher.PubKey
	ServerPk cipher.PubKey
	RoomPk   cipher.PubKey
	Msg      string
}

// SendTextMessage sends a text message to the given route
func (c Handler) SendTextMessage(r SendTextMessageRequestModel) error {

	bytes, err := json.Marshal(r.Msg)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	err = c.chatServices.Commands.SendTextMessageHandler.Handle(commands.SendTextMessageRequest{
		Route: util.NewRoomRoute(r.VisorPk, r.ServerPk, r.RoomPk),
		Msg:   bytes,
	})
	return err
}
