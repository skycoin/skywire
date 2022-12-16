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

// handleRemoteServerMessage handles all messages from a remote server/room
func (ms MessengerService) handleRemoteServerMessage(m message.Message) error {
	//TODO: check if we are member -> if not ignore message

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

	switch m.GetMessageType() {
	case message.ConnMsgType:
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
		err = ms.handleRemoteRoomConnMsgType(m)
		if err != nil {
			fmt.Println(err)
		}
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
		err = ms.handleRemoteRoomInfoMsgType(visor, m)
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
		err := ms.handleRemoteRoomTextMsgType(visor, m)
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

// handleRemoteRoomConnMsgType handles all messages of type ConnMsgtype of remote servers
func (ms MessengerService) handleRemoteRoomConnMsgType(m message.Message) error {

	//Get user to get the info
	user, err := ms.usrRepo.GetUser()
	if err != nil {
		return err
	}

	//the root route of this server (== the Destination of the message)
	root := m.Dest
	//the destination route of a message to send back to the root
	dest := m.Root

	switch m.MsgSubtype {
	case message.ConnMsgTypeAccept:
		//notify that we received an accept message
		n := notification.NewMsgNotification(m.Root, m)
		err := ms.ns.Notify(n)
		if err != nil {
			return err
		}
		//as the remote route has accepted the chat request we now can send our info
		err = ms.SendInfoMessage(root, dest, *user.GetInfo())
		if err != nil {
			return err
		}

	case message.ConnMsgTypeReject:
		n := notification.NewMsgNotification(m.Root, m)
		err := ms.ns.Notify(n)
		if err != nil {
			return err
		}
		//? do we have to delete something here?
		return nil

	default:
		return fmt.Errorf("incorrect data received")
	}
	return nil

}

// handleRemoteRoomInfoMsgType handles messages of type info of peers
func (ms MessengerService) handleRemoteRoomInfoMsgType(v *chat.Visor, m message.Message) error {
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

	return nil
}

// handleRemoteRoomTextMstType handles messages of type text of the remote chat
func (ms MessengerService) handleRemoteRoomTextMsgType(c *chat.Visor, m message.Message) error {

	//notify about a new TextMessage
	n := notification.NewMsgNotification(util.NewP2PRoute(c.GetPK()), message.NewTextMessage(m.Root.Visor, m.Dest, m.Message))
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}
