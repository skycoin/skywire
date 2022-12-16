// Package commands contains commands to delete a remote visor
package commands

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
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
	cliRepo   client.Repository
	visorRepo chat.Repository
}

// NewDeleteRemoteVisorRequestHandler Handler constructor
func NewDeleteRemoteVisorRequestHandler(cliRepo client.Repository, visorRepo chat.Repository) DeleteRemoteVisorRequestHandler {
	return deleteRemoteVisorRequestHandler{cliRepo: cliRepo, visorRepo: visorRepo}
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

	//[]: Send 'Delete-Visor-Message' to remote visor so it can be notified about the deletion??

	//close all routes
	//TODO:this does not work as expected --> shouldnt only the routes be closed?
	pCli.GetAppClient().Close()

	return h.visorRepo.Delete(command.Pk)
}
