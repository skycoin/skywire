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

// handleP2PMessage handles messages received as direct message
func (ms MessengerService) handleP2PMessage(m message.Message) error {
	fmt.Println("handleP2PMessage")

	pkroute := util.NewP2PRoute(m.Root.Visor)

	//first check if the message is of type ConnMsgType
	//we need to handle this first, as we first have to accept or reject a message
	if m.GetMessageType() == message.ConnMsgType {
		err := ms.handleP2PConnMsgType(m)
		if err != nil {
			return err
		}
		return nil
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
	dest := pkroute

	if usr.GetSettings().InBlacklist(pkroute.Visor) {
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

	//now we can handle all other message-types
	switch m.GetMessageType() {
	case message.InfoMsgType:
		//add the message to the p2p chat and update visor & repository
		visor.AddMessage(pkroute, m)
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
		visor.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err := ms.handleP2PTextMsgType(m)
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
	fmt.Println("handleP2PConnMsgType")

	pkroute := util.NewP2PRoute(m.Root.Visor)

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		return err
	}

	//the p2p route of the user
	root := util.NewP2PRoute(usr.GetInfo().GetPK())
	//the p2p route of the peer
	dest := pkroute

	switch m.MsgSubtype {
	case message.ConnMsgTypeRequest:
		//check if sender is in blacklist, if not send accetp and info messages back, else send reject message
		if !usr.GetSettings().InBlacklist(pkroute.Visor) {
			// check if visor exists in repository -> it is possible that we already have got the peer visor saved as a host of a server
			v, err := ms.visorRepo.GetByPK(pkroute.Visor)
			if err != nil {
				//make new default visor with a default p2p-room and save it in the visor repository
				fmt.Println("Make new P2P visor")
				v2 := chat.NewDefaultP2PVisor(pkroute.Visor)
				err = ms.visorRepo.Add(v2)
				if err != nil {
					return err
				}
				v = &v2
			}

			//check if p2p already exists in repository
			if v.P2PIsEmpty() {
				//make new default p2p room and add it to the visor
				fmt.Println("Make new P2P room")
				p2p := chat.NewDefaultP2PRoom(pkroute.Visor)
				err = v.AddP2P(p2p)
				if err != nil {
					return err
				}
			}

			//add request message to p2p
			v.AddMessage(pkroute, m)
			//update repo with visor
			err = ms.visorRepo.Set(*v)
			if err != nil {
				return err
			}

			//notify about the new chat initiated by a remote visor
			n := notification.NewP2PChatNotification(pkroute.Visor)
			err = ms.ns.Notify(n)
			if err != nil {
				return err
			}

			//send a chat-accept-message to the remote peer
			err = ms.SendChatAcceptMessage(pkroute, root, dest)
			if err != nil {
				return err
			}

			//send the users info to the remote peer
			err = ms.SendInfoMessage(pkroute, root, dest, *usr.GetInfo())
			if err != nil {
				return err
			}

		} else {
			//sends a chat-reject-message to the remote peer
			err = ms.SendChatRejectMessage(root, dest)
			if err != nil {
				return err
			}

			// we first have to check whether we don't have got any other servers of this visor saved
			v, err := ms.visorRepo.GetByPK(pkroute.Visor)
			if err != nil {
				return err
			}
			if len(v.GetAllServer()) != 0 {
				return nil
			}

			//deletes the visor from the repository if no other servers of the visor are saved
			err = ms.visorRepo.Delete(pkroute.Visor)
			if err != nil {
				return err
			}
			return fmt.Errorf("pk in blacklist rejected")
		}
	case message.ConnMsgTypeAccept:
		//get the visor
		v, err := ms.visorRepo.GetByPK(pkroute.Visor)
		if err != nil {
			return err
		}

		//add request message to visor route
		v.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*v)
		if err != nil {
			return err
		}

		//notify that we received an accept message
		n := notification.NewMsgNotification(pkroute, m)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
		//as the peer has accepted the chat request we now can send our info
		err = ms.SendInfoMessage(pkroute, root, dest, *usr.GetInfo())
		if err != nil {
			return err
		}

	case message.ConnMsgTypeReject:
		n := notification.NewMsgNotification(pkroute, m)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
	case message.ConnMsgTypeDelete, message.ConnMsgTypeLeave:
		//get the visor
		v, err := ms.visorRepo.GetByPK(pkroute.Visor)
		if err != nil {
			return err
		}

		//add request message to visor route
		v.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*v)
		if err != nil {
			return err
		}

		//notify that we received an accept message
		n := notification.NewMsgNotification(pkroute, m)
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
	fmt.Println("handleP2PInfoMsgType")

	pkroute := util.NewP2PRoute(m.Root.Visor)

	//unmarshal the received message bytes to info.Info
	i := info.Info{}
	err := json.Unmarshal(m.Message, &i)
	if err != nil {
		return fmt.Errorf("failed to unmarshal json message: %v", err)
	}
	fmt.Println("---------------------------------------------------------------------------------------------------")
	fmt.Printf("InfoMessage: \n")
	fmt.Printf("Pk:		%s \n", i.Pk.Hex())
	fmt.Printf("Alias:	%s \n", i.Alias)
	fmt.Printf("Desc:	%s \n", i.Desc)
	fmt.Printf("Img:	%s \n", i.Img)
	fmt.Println("---------------------------------------------------------------------------------------------------")

	//update the info of the p2p
	err = v.SetRouteInfo(pkroute, i)
	if err != nil {
		return err
	}
	err = ms.visorRepo.Set(*v)
	if err != nil {
		return err
	}

	//notify about new info message
	n := notification.NewMsgNotification(pkroute, m)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// handleP2PTextMstType handles messages of type text of the p2p chat
func (ms MessengerService) handleP2PTextMsgType(m message.Message) error {
	fmt.Println("handleP2PTextMsgType")

	pkroute := util.NewP2PRoute(m.Root.Visor)

	fmt.Println("---------------------------------------------------------------------------------------------------")
	fmt.Printf("TextMessage: \n")
	fmt.Printf("Text:	%s \n", m.Message)
	fmt.Println("---------------------------------------------------------------------------------------------------")

	//notify about a new TextMessage
	n := notification.NewMsgNotification(pkroute, m)
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}
