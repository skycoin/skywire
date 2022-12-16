package netcon

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// handleLocalServerMessage handles messages received to a local route (server/room)
// these can also be locally sent messages from the user to his own local route
func (ms MessengerService) handleLocalServerMessage(m message.Message) error {
	//first check if the message is of type ConnMsgType
	//we need to handle this first, as we first have to accept or reject a message
	if m.GetMessageType() == message.ConnMsgType {
		err := ms.handleLocalRoomConnMsgType(m)
		if err != nil {
			return err
		}
	}

	//if the message is not of type ConnMsgType check if the remote pk is blacklisted
	// to prevent a peer from sending other messages before a connection-request message
	visor, err := ms.visorRepo.GetByPK(m.Dest.Visor)
	if err != nil {
		return err
	}
	server, err := visor.GetServerByPK(m.Dest.Server)
	if err != nil {
		return err
	}
	r, err := server.GetRoomByPK(m.Dest.Room)
	if err != nil {
		return err
	}

	//save room as local variable
	room := *r

	//the root route of this server (== the Destination of the message)
	root := m.Dest
	//the destination route of a message to send back to the root
	dest := m.Root

	//check if in blacklist of server //TODO: or not a member !! add always local as member
	if _, ok := server.GetBlacklist()[m.Root.Visor]; ok {
		err = ms.SendChatRejectMessage(root, dest)
		if err != nil {
			return err
		}
		return fmt.Errorf("Message rejected from " + m.Root.Visor.String())
	}

	//check if in blacklist of room  //TODO: or ot a member
	if _, ok := room.GetBlacklist()[m.Root.Visor]; ok {
		err = ms.SendChatRejectMessage(root, dest)
		if err != nil {
			return err
		}
		return fmt.Errorf("Message rejected from " + m.Root.Visor.String())
	}

	//check if in muted of server
	//TODO:
	//check if in muted of room
	//TODO:

	//now we can handle all other message-types
	switch m.GetMessageType() {
	case message.InfoMsgType:
		//add the message to the room and update server, visor & repository
		room.AddMessage(m)
		err = server.SetRoom(room)
		if err != nil {
			return err
		}
		err = visor.SetServer(*server)
		if err != nil {
			return err
		}
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err = ms.handleLocalRoomInfoMsgType(visor, m)
		if err != nil {
			fmt.Println(err)
		}
	case message.TxtMsgType:
		//add the message to the room and update server, visor & repository
		room.AddMessage(m)
		err = server.SetRoom(room)
		if err != nil {
			return err
		}
		err = visor.SetServer(*server)
		if err != nil {
			return err
		}
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err := ms.handleLocalRoomTextMsgType(visor, m)
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

// handleRoomConnMsgType handles an incoming connection message and either accepts it and sends back the own info as message
// or if the public key is in the blacklist rejects the chat request.
func (ms MessengerService) handleLocalRoomConnMsgType(m message.Message) error {
	visor, err := ms.visorRepo.GetByPK(m.Dest.Visor)
	if err != nil {
		return err
	}
	server, err := visor.GetServerByPK(m.Dest.Server)
	if err != nil {
		return err
	}
	r, err := server.GetRoomByPK(m.Dest.Room)
	if err != nil {
		return err
	}

	//save room as local variable
	room := *r

	//the root route of this server (== the Destination of the message)
	root := m.Dest
	//the destination route of a message to send back to the root
	dest := m.Root

	switch m.MsgSubtype {
	case message.ConnMsgTypeRequest:
		//check if sender is in blacklist, if not send accept and info messages back, else send reject message
		if _, ok := server.GetBlacklist()[m.Root.Visor]; !ok {
			if _, ok2 := room.GetBlacklist()[m.Root.Visor]; !ok2 {

				//TODO: Add chat request message to room

				//send a chat-accept-message to the remote peer
				err = ms.SendChatAcceptMessage(root, dest)
				if err != nil {
					return err
				}

				//send the rooms info to the remote peer
				err = ms.SendInfoMessage(root, dest, room.GetInfo())
				if err != nil {
					return err
				}

				//add remote peer to members so he is able to send other messages than connMsgType
				info := info.NewDefaultInfo()
				info.Pk = m.Root.Visor
				dummyPeer := peer.NewPeer(info, "")
				//add remote peer to room
				err = room.AddMember(*dummyPeer)
				if err != nil {
					return err
				}
				//update room inside server
				err = server.SetRoom(room)
				if err != nil {
					return err
				}
				//add remote peer to server
				err = server.AddMember(*dummyPeer)
				if err != nil {
					return err
				}
				//update server inside visor
				err = visor.SetServer(*server)
				if err != nil {
					return err
				}
				//update visor inside repository
				err = ms.visorRepo.Set(*visor)
				if err != nil {
					return err
				}

				//TODO: send new member-list to members

				//TODO: notify about new member

			} else {
				//sends a chat-reject-message to the remote peer
				err = ms.SendChatRejectMessage(root, dest)
				if err != nil {
					return err
				}
				return fmt.Errorf("pk in room-blacklist rejected")
			}
		} else {
			//sends a chat-reject-message to the remote peer
			err = ms.SendChatRejectMessage(root, dest)
			if err != nil {
				return err
			}
			return fmt.Errorf("pk in server-blacklist rejected")
		}
	default:
		return fmt.Errorf("incorrect data received")

	}

	return nil
}

// handleLocalRoomInfoMsgType handles messages of type info of peers
func (ms MessengerService) handleLocalRoomInfoMsgType(v *chat.Visor, m message.Message) error {
	//unmarshal the received message bytes to info.Info
	i := info.Info{}
	err := json.Unmarshal(m.Message, &i)
	if err != nil {
		return fmt.Errorf("failed to unmarshal json message: %v", err)
	}
	s, err := v.GetServerByPK(m.GetDestinationServer())
	if err != nil {
		return err
	}

	//update the info of the member in the server and all rooms
	s.SetMemberInfo(i)
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

	//TODO: send updated info of members to all members

	return nil
}

// handleLocalRoomTextMstType handles messages of type text of the p2p chat
func (ms MessengerService) handleLocalRoomTextMsgType(c *chat.Visor, m message.Message) error {

	//notify about a new TextMessage
	n := notification.NewMsgNotification(util.NewP2PRoute(c.GetPK()), message.NewTextMessage(m.Root.Visor, m.Dest, m.Message))
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	//TODO: send message to all members

	return nil
}
