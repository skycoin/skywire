package commands

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
)

//AddChatRequest of AddChatRequestHandler
type AddChatRequest struct {
	Pk cipher.PubKey
}

//AddChatRequestHandler struct that allows handling AddChatRequest
type AddChatRequestHandler interface {
	Handle(command AddChatRequest) error
}

type addChatRequestHandler struct {
	messengerService messenger.Service
	chatRepo         chat.Repository
}

//NewAddChatRequestHandler Initializes an AddCommandHandler
func NewAddChatRequestHandler(chatRepo chat.Repository, messengerService messenger.Service) AddChatRequestHandler {
	return addChatRequestHandler{chatRepo: chatRepo, messengerService: messengerService}
}

//Handle Handles the AddChatRequest
func (h addChatRequestHandler) Handle(req AddChatRequest) error {
	fmt.Println("AddChatHandler - Request: " + req.Pk.Hex())
	//1. check if the pubkey is already in chats
	_, err := h.chatRepo.GetByPK(req.Pk)
	if err == nil {
		return fmt.Errorf("chat %s already added", req.Pk.Hex())
	}

	err = h.messengerService.SendChatRequestMessage(req.Pk)
	if err != nil {
		return err
	}

	go h.messengerService.Handle(req.Pk) //nolint:errcheck

	return nil
}
