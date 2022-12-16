package netcon

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// handleP2PMessage handles messages sent as direct message
func (ms MessengerService) handleP2PMessage(m message.Message) error {

	//first check if the message is of type ConnMsgType
	//we need to handle this first, as we first have to accept or reject a message
	if m.GetMessageType() == message.ConnMsgType {
		err := ms.handleP2PConnMsgType(m)
		if err != nil {
			return err
		}
	}
	//if the message is not of type ConnMsgType check if the remote pk is blacklisted
	// to prevent a peer from sending other messages before a connection-request message
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		return err
	}

	//the p2p route of the user
	root := util.NewP2PRoute(usr.GetInfo().GetPK())
	//the p2p route of the peer
	dest := util.NewP2PRoute(m.Root.Visor)

	if usr.GetSettings().InBlacklist(m.Root.Visor) {
		err = ms.SendChatRejectMessage(root, dest)
		if err != nil {
			return err
		}
		return fmt.Errorf("Message rejected from " + m.Root.Visor.String())
	}

	//get the current p2p-room so when updating nothing gets overwritten
	visor, err := ms.visorRepo.GetByPK(m.Root.Visor)
	if err != nil {
		return err
	}
	p2p, err := visor.GetP2P()
	if err != nil {
		return err
	}

	//now we can handle all other message-types
	switch m.GetMessageType() {
	case message.InfoMsgType:
		//add the message to the p2p chat and update visor & repository
		p2p.AddMessage(m)
		err = visor.SetP2P(p2p)
		if err != nil {
			return err
		}
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err = ms.handleP2PInfoMsgType(visor, m)
		if err != nil {
			fmt.Println(err)
		}
	case message.TxtMsgType:
		//add the message to the p2p chat and update visor & repository
		p2p.AddMessage(m)
		err = visor.SetP2P(p2p)
		if err != nil {
			return err
		}
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err := ms.handleP2PTextMsgType(visor, m)
		if err != nil {
			return err
		}
	case message.CmdMsgType:
		return fmt.Errorf("commands are not allowed on p2p chats")
	default:
		return fmt.Errorf("incorrect data received")
	}
	return nil

}

// handleP2PConnMsgType handles an incoming connection message and either accepts it and sends back the own info as message
// or if the public key is in the blacklist rejects the chat request.
func (ms MessengerService) handleP2PConnMsgType(m message.Message) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		return err
	}

	//the p2p route of the user
	root := util.NewP2PRoute(usr.GetInfo().GetPK())
	//the p2p route of the peer
	dest := util.NewP2PRoute(m.Root.Visor)

	switch m.MsgSubtype {
	case message.ConnMsgTypeRequest:
		//check if sender is in blacklist, if not send accetp and info messages back, else send reject message
		if !usr.GetSettings().InBlacklist(m.Root.Visor) {
			//make new default visor with a default p2p-room and save it in the visor repository
			v := chat.NewDefaultP2PVisor(m.Root.Visor)
			err = ms.visorRepo.Add(v)
			if err != nil {
				return err
			}

			//TODO: Add request message to p2p

			//notify about the new chat initiated by a remote visor
			n := notification.NewChatNotification(m.Root.Visor)
			err = ms.ns.Notify(n)
			if err != nil {
				return err
			}

			//sends a chat-accept-message to the remote peer
			err = ms.SendChatAcceptMessage(root, dest)
			if err != nil {
				return err
			}

			//sends the users info to the remote peer
			err = ms.SendInfoMessage(root, dest, *usr.GetInfo())
			if err != nil {
				return err
			}

		} else {
			//sends a chat-reject-message to the remote peer
			err = ms.SendChatRejectMessage(root, dest)
			if err != nil {
				return err
			}
			//deletes the visor from the repository
			err = ms.visorRepo.Delete(m.Root.Visor)
			if err != nil {
				return err
			}
			return fmt.Errorf("pk in blacklist rejected")
		}
	case message.ConnMsgTypeAccept:
		//notify that we received an accept message
		n := notification.NewMsgNotification(m.Root, m)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
		//as the peer has accepted the chat request we now can send our info
		err = ms.SendInfoMessage(root, dest, *usr.GetInfo())
		if err != nil {
			return err
		}

	case message.ConnMsgTypeReject:
		n := notification.NewMsgNotification(m.Root, m)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("incorrect data received")
	}

	return nil
}

// handleP2PInfoMsgType handles messages of type info of the p2p chat
func (ms MessengerService) handleP2PInfoMsgType(v *chat.Visor, m message.Message) error {
	//unmarshal the received message bytes to info.Info
	i := info.Info{}
	err := json.Unmarshal(m.Message, &i)
	if err != nil {
		return fmt.Errorf("failed to unmarshal json message: %v", err)
	}
	//update the info of the p2p
	v.P2P.Info = i
	err = ms.visorRepo.Set(*v)
	if err != nil {
		return err
	}

	//notify about new info message
	n := notification.NewMsgNotification(util.NewP2PRoute(v.GetPK()), m)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// handleP2PTextMstType handles messages of type text of the p2p chat
func (ms MessengerService) handleP2PTextMsgType(c *chat.Visor, m message.Message) error {

	//notify about a new TextMessage
	n := notification.NewMsgNotification(util.NewP2PRoute(c.GetPK()), message.NewTextMessage(m.Root.Visor, m.Dest, m.Message))
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}
