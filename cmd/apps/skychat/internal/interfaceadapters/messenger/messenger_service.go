// Package messengerimpl implements a messenger service to handle received and sent messages
package messengerimpl

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/connectionhandler"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// MessengerService provides a netcon implementation of the Service
type MessengerService struct {
	ns        notification.Service
	cliRepo   client.Repository
	usrRepo   user.Repository
	visorRepo chat.Repository
	ch        connectionhandler.Service
	errs      chan error
}

// NewMessengerService constructor for MessengerService
func NewMessengerService(ns notification.Service, cR client.Repository, uR user.Repository, chR chat.Repository, ch connectionhandler.Service) *MessengerService {
	ms := MessengerService{}

	ms.ns = ns
	ms.cliRepo = cR
	ms.usrRepo = uR
	ms.visorRepo = chR
	ms.ch = ch

	ms.errs = make(chan error, 1)

	go ms.HandleReceivedMessages()

	return &ms
}

// HandleReceivedMessages waits for incoming messages on channel and then handles them
func (ms MessengerService) HandleReceivedMessages() {
	for msg := range ms.ch.GetReceiveChannel() {
		err := ms.HandleReceivedMessage(msg)
		if err != nil {
			ms.errs <- err
		}
	}
}

// HandleReceivedMessage handles a received message
func (ms MessengerService) HandleReceivedMessage(receivedMessage message.Message) error {
	chatClient, err := ms.cliRepo.GetClient()
	if err != nil {
		return err
	}

	localPK := chatClient.GetAppClient().Config().VisorPK

	if receivedMessage.IsFromRemoteP2PToLocalP2P(localPK) {
		go ms.handleP2PMessage(receivedMessage)
	} else if receivedMessage.IsFromRemoteServer() {
		go ms.handleRemoteServerMessage(receivedMessage)
	} else if receivedMessage.IsFromRemoteToLocalServer(localPK) {
		go ms.handleLocalServerMessage(receivedMessage)
	} else {
		return fmt.Errorf("received message that can't be matched to remote server, local server or p2p chat")
	}
	return nil
}

// sendMessageAndSaveItToDatabase sends a message and saves it to the database
func (ms MessengerService) sendMessageAndSaveItToDatabase(pkroute util.PKRoute, m message.Message) error {
	return ms.ch.SendMessage(pkroute, m, true)
}

// sendMessageAndDontSaveItToDatabase sends a message but doesn't save it to the database
func (ms MessengerService) sendMessageAndDontSaveItToDatabase(pkroute util.PKRoute, m message.Message) error {
	return ms.ch.SendMessage(pkroute, m, false)
}

// sendMessageToRemoteRoute sends the given message to a remote route (as p2p and client)
func (ms MessengerService) sendMessageToRemoteRoute(msg message.Message) error {
	err := ms.sendMessageAndSaveItToDatabase(msg.Dest, msg)
	if err != nil {
		return err
	}

	n := notification.NewMsgNotification(msg.Dest)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// sendMessageToLocalRoute "sends" the message to local server, so local server handles it, as it was sent from a remote route (used for messages send from server host, but as client)
func (ms MessengerService) sendMessageToLocalRoute(msg message.Message) error {
	msg.Status = message.MsgStatusSent
	go ms.handleLocalServerMessage(msg)

	return nil
}
