package netcon

import (
	"encoding/json"
	"fmt"
	"time"

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
	fmt.Println("handleLocalServerMessage")

	pkroute := util.NewRoomRoute(m.GetDestinationVisor(), m.GetDestinationServer(), m.GetDestinationRoom())

	//Check if visor exists
	visor, err := ms.visorRepo.GetByPK(m.Dest.Visor)
	if err != nil {
		return err
	}

	//Check if server exists
	server, err := visor.GetServerByPK(m.Dest.Server)
	if err != nil {
		return err
	}

	//check if the message is of type ConnMsgType
	//we need to handle this first, as we first have to accept or reject a message
	if m.GetMessageType() == message.ConnMsgType {
		err := ms.handleLocalServerConnMsgType(visor, m)
		if err != nil {
			return err
		}
		return nil
	}

	//the root route of this server
	root := pkroute
	//the destination route of a message to send back to the root
	dest := m.Root

	//check if origin of message is a member or in blacklist of server //TODO: or not a member !! add always local as member
	if _, ok := server.GetBlacklist()[m.Root.Visor]; ok {
		err = ms.SendChatRejectMessage(root, dest)
		if err != nil {
			//?:Maybe better continue than throwing an error as we don't care if the sending didnt work?
			return err
		}
		//? Maybe better continue and log the event?
		return fmt.Errorf("Message rejected from " + m.Root.Visor.String())
	}

	//handle Command Messages
	if m.GetMessageType() == message.CmdMsgType {
		err := ms.handleLocalServerCmdMsgType(visor, m)
		if err != nil {
			return err
		}
		return nil
	}

	//Check if room exists
	r, err := server.GetRoomByPK(pkroute.Room)
	if err != nil {
		return err
	}

	//check if origin of message is member or in blacklist of room  //TODO: or not a member
	if _, ok := r.GetBlacklist()[m.Root.Visor]; ok {
		err = ms.SendChatRejectMessage(root, dest)
		if err != nil {
			return err
		}
		return fmt.Errorf("Message rejected from " + m.Root.Visor.String())
	}

	//now we can handle all other message-types
	switch m.GetMessageType() {
	case message.InfoMsgType:
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err = ms.handleLocalServerInfoMsgType(visor, m)
		if err != nil {
			fmt.Println(err)
		}
	case message.TxtMsgType:
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			return err
		}
		//handle the message
		err := ms.handleLocalRoomTextMsgType(visor, m)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("incorrect data received")

	}
	return nil
}

// handleLocalServerConnMsgType handles an incoming connection message and either accepts it and sends back the own info as message
// or if the public key is in the blacklist rejects the chat request.
func (ms MessengerService) handleLocalServerConnMsgType(visor *chat.Visor, m message.Message) error {
	fmt.Println("handleLocalServerConnMsgType")

	pkroute := util.NewRoomRoute(m.GetDestinationVisor(), m.GetDestinationServer(), m.GetDestinationRoom())

	server, err := visor.GetServerByPK(m.Dest.Server)
	if err != nil {
		return err
	}
	r, err := server.GetRoomByPK(m.Dest.Room)
	if err != nil {
		return err
	}

	//the root route of this server (== the Destination of the message)
	root := pkroute
	//the destination route of a message to send back to the root
	dest := m.Root

	switch m.MsgSubtype {
	case message.ConnMsgTypeRequest:
		//check if sender is in blacklist, if not send accept and info messages back, else send reject message
		if _, ok := server.GetBlacklist()[m.Root.Visor]; !ok {
			if _, ok2 := r.GetBlacklist()[m.Root.Visor]; !ok2 {

				//add request message to room
				r.AddMessage(m)

				//send request message to peers
				ms.sendMessageToPeers(visor, m)

				//add remote peer to members so he is able to send other messages than connMsgType
				info := info.NewDefaultInfo()
				info.Pk = m.Root.Visor
				dummyPeer := peer.NewPeer(info, "")

				//add remote peer to server
				err = server.AddMember(*dummyPeer)
				if err != nil {
					return err
				}
				//add remote peer to room
				err = r.AddMember(*dummyPeer)
				if err != nil {
					return err
				}
				//update room inside server
				err = server.SetRoom(*r)
				if err != nil {
					return err
				}
				//update server inside visor
				err = visor.SetServer(*server)
				if err != nil {
					return err
				}
				//update visorRepo
				err = ms.visorRepo.Set(*visor)
				if err != nil {
					return err
				}

				//notify about a new messages/infos inside a group chat
				n := notification.NewGroupChatNotification(pkroute)
				err = ms.ns.Notify(n)
				if err != nil {
					return err
				}

				//send a chat-accept-message to the remote peer
				err = ms.SendChatAcceptMessage(pkroute, root, dest)
				if err != nil {
					return err
				}

				//!! FIXME
				//This sleep is necessary, as it looks like the transport hangs up when sending two messages in a short time frame
				time.Sleep(1 * time.Second)

				//send the rooms info to the remote peer
				err = ms.SendInfoMessage(pkroute, root, dest, r.GetInfo())
				if err != nil {
					return err
				}

				//TODO: send new member-list to members

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

// handleLocalServerCmdMsgType handles messages of type cmd of peers(admins/moderators)
func (ms MessengerService) handleLocalServerCmdMsgType(visor *chat.Visor, m message.Message) error {
	fmt.Println("handleLocalServerCmdMsgType")

	pkroute := util.NewRoomRoute(m.GetDestinationVisor(), m.GetDestinationServer(), m.GetDestinationRoom())

	//check if server exists
	server, err := visor.GetServerByPK(m.Dest.Server)
	if err != nil {
		return err
	}

	//get user
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	//unmarshal the received message bytes to info.Info
	i := info.Info{}
	err = json.Unmarshal(m.Message, &i)
	if err != nil {
		return fmt.Errorf("failed to unmarshal json message: %v", err)
	}

	switch m.MsgSubtype {
	case message.CmdMsgTypeAddRoom:
		//TODO: First check if origin of msg is admin
		// make a new route
		rr := util.NewLocalRoomRoute(m.Dest.Visor, m.Dest.Server, server.GetAllRoomsBoolMap())

		// setup room for repository
		r := chat.NewLocalRoom(rr, i, chat.DefaultRoomType)

		//setup user as peer for room membership
		p := peer.NewPeer(*usr.GetInfo(), usr.GetInfo().Alias)
		//Add user as member
		err = r.AddMember(*p)
		if err != nil {
			return err
		}

		//TODO:: if room is visible/public also add messengerService and send 'Room-Added' Message to Members of server

		// add room to server, update visor and then update repository
		err = server.AddRoom(r)
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

		//notify about the added route
		n := notification.NewAddRouteNotification(pkroute)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
		return nil
	case message.CmdMsgTypeDeleteRoom:
		//TODO: First check if origin of msg is admin
		//TODO: handle message
		return nil
		//TODO: add other cmd messages
	default:
		return fmt.Errorf("incorrect data received")
	}
}

// handleLocalServerInfoMsgType handles messages of type info of peers
func (ms MessengerService) handleLocalServerInfoMsgType(v *chat.Visor, m message.Message) error {
	fmt.Println("handleLocalServerInfoMsgType")

	pkroute := util.NewRoomRoute(m.GetDestinationVisor(), m.GetDestinationServer(), m.GetDestinationRoom())

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

	//get server from visor
	s, err := v.GetServerByPK(pkroute.Server)
	if err != nil {
		return err
	}

	//update the info of the member in the server and all rooms
	err = s.SetMemberInfo(i)
	if err != nil {
		return err
	}
	err = v.SetServer(*s)
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

	//TODO: send updated info of members to all members

	return nil
}

// handleLocalRoomTextMstType handles messages of type text of the p2p chat
func (ms MessengerService) handleLocalRoomTextMsgType(v *chat.Visor, m message.Message) error {
	fmt.Println("handleLocalRoomTextMsgType")

	pkroute := util.NewRoomRoute(m.GetDestinationVisor(), m.GetDestinationServer(), m.GetDestinationRoom())

	//check if in muted of server
	//TODO:
	//check if in muted of room
	//TODO:

	//notify about a new TextMessage
	n := notification.NewMsgNotification(pkroute, m)
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	err = ms.sendMessageToPeers(v, m)
	if err != nil {
		return err
	}

	return nil
}

//
func (ms MessengerService) sendMessageToPeers(v *chat.Visor, m message.Message) error {
	fmt.Println("sendMessageToPeers")

	pkroute := util.NewRoomRoute(m.GetDestinationVisor(), m.GetDestinationServer(), m.GetDestinationRoom())

	server, err := v.GetServerByPK(m.Dest.Server)
	if err != nil {
		return err
	}

	r, err := server.GetRoomByPK(m.Dest.Room)
	if err != nil {
		return err
	}

	members := r.GetAllMembers()

	for _, peer := range members {

		//only send to remote peers and not to ourself
		if peer.GetPK() != pkroute.Visor {

			m.Root = pkroute
			m.Dest = util.NewP2PRoute(peer.GetPK())

			err := ms.sendMessage(pkroute, m, false)
			if err != nil {
				fmt.Printf("error sending group message to peer: %v", err)
				continue
			}
		}
	}
	return nil
}
