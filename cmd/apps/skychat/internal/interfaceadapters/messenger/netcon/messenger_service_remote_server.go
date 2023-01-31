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
	fmt.Println("handleRemoteServerMessage")

	pkroute := util.NewRoomRoute(m.GetRootVisor(), m.GetRootServer(), m.GetRootRoom())

	//TODO: check if we are member -> if not ignore message

	visor, err := ms.visorRepo.GetByPK(m.Root.Visor)
	if err != nil {
		return err
	}

	switch m.GetMessageType() {
	case message.ConnMsgType:
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
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
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
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
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
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
	fmt.Println("handleRemoteRoomConnMsgType")

	pkroute := util.NewRoomRoute(m.GetRootVisor(), m.GetRootServer(), m.GetRootRoom())

	//Get user to get the info
	user, err := ms.usrRepo.GetUser()
	if err != nil {
		return err
	}

	//the root route of this user
	root := util.NewP2PRoute(user.GetInfo().GetPK())
	//the destination route of a message to send back to the root
	dest := pkroute

	switch m.MsgSubtype {
	case message.ConnMsgTypeAccept:
		//notify that we received an accept message
		n := notification.NewMsgNotification(m.Root, m)
		err := ms.ns.Notify(n)
		if err != nil {
			return err
		}
		//as the remote route has accepted the chat request we now can send our info
		err = ms.SendInfoMessage(pkroute, root, dest, *user.GetInfo())
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
	fmt.Println("handleRemoteRoomInfoMsgType")
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

	//TODO: ?? Put something like SetRoomInfo into visor as method so only a call of visor.SetRoomInfo etc. is needed everywhere and not getting server and then getting room??
	//get server from visor
	s, err := v.GetServerByPK(m.GetRootServer())
	if err != nil {
		return err
	}

	//get room from server
	r, err := s.GetRoomByPK(m.GetRootRoom())
	if err != nil {
		return err
	}

	//update the info of the remote server
	r.SetInfo(i) //TODO: return error?
	err = s.SetRoom(*r)
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
	n := notification.NewMsgNotification(m.Root, m)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// handleRemoteRoomTextMstType handles messages of type text of the remote chat
func (ms MessengerService) handleRemoteRoomTextMsgType(c *chat.Visor, m message.Message) error {
	fmt.Println("handleRemoteRoomTextMsgType")

	fmt.Println("---------------------------------------------------------------------------------------------------")
	fmt.Printf("TextMessage: \n")
	fmt.Printf("Text:	%s \n", m.Message)
	fmt.Println("---------------------------------------------------------------------------------------------------")

	//notify about a new TextMessage
	n := notification.NewMsgNotification(util.NewP2PRoute(c.GetPK()), message.NewTextMessage(m.Root.Visor, m.Dest, m.Message))
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}
