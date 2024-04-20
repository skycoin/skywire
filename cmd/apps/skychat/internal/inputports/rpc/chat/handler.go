// Package chat contains code of the rpc handler for inputports
package chat

import (
	"encoding/json"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	chatservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Handler chat request handler
type Handler struct {
	chatServices chatservices.ChatServices
	log          *logging.Logger
}

// NewHandler Constructor returns *Handler
func NewHandler(cs chatservices.ChatServices) *Handler {
	return &Handler{chatServices: cs, log: logging.MustGetLogger("chat:rpc")}
}

// SendTextMessageRPCParam contains the parameter identifier to be parsed by the handler
const SendTextMessageRPCParam = "Handler" + "." + "SendTextMessage"

// SendTextMessageRequestModel represents the request model expected for send text request
type SendTextMessageRequestModel struct {
	VisorPk  cipher.PubKey
	ServerPk cipher.PubKey
	RoomPk   cipher.PubKey
	Msg      string
}

// SendTextMessage sends a text message to the given route
func (c Handler) SendTextMessage(r SendTextMessageRequestModel, _ *int) error {

	c.log.Debugln("SendTextMessage via cli")
	c.log.Debugln("Message: %s\n v: %s\n s: %s\n r: %s\n", r.Msg, r.VisorPk.Hex(), r.ServerPk.Hex(), r.RoomPk.Hex())

	//TODO: maybe check if route is available first and depending on result first send join remote route

	bytes, err := json.Marshal(r.Msg)
	if err != nil {
		c.log.Errorf("Failed to marshal json: %v", err)
		return err
	}

	err = c.chatServices.Commands.SendTextMessageHandler.Handle(commands.SendTextMessageRequest{
		Route: util.NewRoomRoute(r.VisorPk, r.ServerPk, r.RoomPk),
		Msg:   bytes,
	})
	return err
}
