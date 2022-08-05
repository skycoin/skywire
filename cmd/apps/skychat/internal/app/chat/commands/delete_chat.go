package commands

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
)

//DeleteChatRequest Command Model
type DeleteChatRequest struct {
	Pk cipher.PubKey
}

//DeleteChatRequestHandler Handler Struct with Dependencies
type DeleteChatRequestHandler interface {
	Handle(command DeleteChatRequest) error
}

type deleteChatRequestHandler struct {
	cliRepo  client.Repository
	chatRepo chat.Repository
}

//NewDeleteChatRequestHandler Handler constructor
func NewDeleteChatRequestHandler(cliRepo client.Repository, chatRepo chat.Repository) DeleteChatRequestHandler {
	return deleteChatRequestHandler{cliRepo: cliRepo, chatRepo: chatRepo}
}

//Handle Handles the DeleteChatRequest request
func (h deleteChatRequestHandler) Handle(command DeleteChatRequest) error {
	_, err := h.chatRepo.GetByPK(command.Pk)
	if err != nil {
		return err
	}

	pCli, err := h.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("Error Getting client")
		return err
	}

	//close all routes
	//TODO:this does not work as expected --> shouldnt only the routes be closed?
	pCli.GetAppClient().Close()

	return h.chatRepo.Delete(command.Pk)
}
