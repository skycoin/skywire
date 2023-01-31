// Package commands contains commands to delete a remote visor
package commands

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// DeleteRemoteVisorRequest Command Model
type DeleteRemoteVisorRequest struct {
	Pk cipher.PubKey
}

// DeleteRemoteVisorRequestHandler Handler Struct with Dependencies
type DeleteRemoteVisorRequestHandler interface {
	Handle(command DeleteRemoteVisorRequest) error
}

type deleteRemoteVisorRequestHandler struct {
	cliRepo          client.Repository
	messengerService messenger.Service
	visorRepo        chat.Repository
}

// NewDeleteRemoteVisorRequestHandler Handler constructor
func NewDeleteRemoteVisorRequestHandler(cliRepo client.Repository, visorRepo chat.Repository, messengerService messenger.Service) DeleteRemoteVisorRequestHandler {
	return deleteRemoteVisorRequestHandler{cliRepo: cliRepo, visorRepo: visorRepo, messengerService: messengerService}
}

// Handle Handles the DeleteRemoteVisorRequest request
func (h deleteRemoteVisorRequestHandler) Handle(command DeleteRemoteVisorRequest) error {
	// Check if visor exists
	_, err := h.visorRepo.GetByPK(command.Pk)
	if err != nil {
		return err
	}

	pCli, err := h.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("Error Getting client")
		return err
	}

	// Send 'Leave-Visor-Message' to remote visor so it can be notified about the deletion
	root := util.NewP2PRoute(pCli.GetAppClient().Config().VisorPK)
	dest := util.NewP2PRoute(command.Pk)
	err = h.messengerService.SendChatLeaveMessage(dest, root, dest)
	if err != nil {
		return err
	}

	//get connection
	conn, err := pCli.GetConnByPK(command.Pk)
	if err != nil {
		fmt.Printf("Error getting connection")
		return err
	}

	//close connection
	err = conn.Close()
	if err != nil {
		fmt.Printf("Error closing connection")
		return err
	}

	//delete connection from client
	err = pCli.DeleteConn(command.Pk)
	if err != nil {
		fmt.Printf("Error deleting connection")
		return err
	}

	//update client in repository
	err = h.cliRepo.SetClient(*pCli)
	if err != nil {
		fmt.Printf("Error updating client")
		return err
	}

	//delete visor from visor repository
	h.visorRepo.Delete(command.Pk)
	if err != nil {
		fmt.Printf("Error deleting visor")
		return err
	}

	return nil
}
