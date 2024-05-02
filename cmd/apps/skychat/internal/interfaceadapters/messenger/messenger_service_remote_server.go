package messengerimpl

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// handleRemoteServerMessage handles all messages from a remote server/room
func (ms MessengerService) handleRemoteServerMessage(m message.Message) {
	ms.log.Debugln("handleRemoteServerMessage")

	pkroute := util.NewRoomRoute(m.GetRootVisor(), m.GetRootServer(), m.GetRootRoom())

	//check if we are member of remote server -> if not ignore message
	visor, err := ms.visorRepo.GetByPK(pkroute.Visor)
	if err != nil {
		ms.errs <- fmt.Errorf("message dumped: no member of remote, visor not even known")
		return
	}
	server, err := visor.GetServerByPK(pkroute.Server)
	if err != nil {
		ms.errs <- fmt.Errorf("message dumped: no member of remote server")
		return
	}

	_, err = server.GetRoomByPK(pkroute.Room)
	if err != nil {
		ms.errs <- fmt.Errorf("message dumped: no member of remote room")
		return
	}

	switch m.GetMessageType() {
	case message.ConnMsgType:
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			ms.errs <- err
			return
		}
		//handle the message
		err = ms.handleRemoteRoomConnMsgType(m)
		if err != nil {
			ms.errs <- err
			return
		}
	case message.InfoMsgType:
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			ms.errs <- err
			return
		}
		//handle the message
		err = ms.handleRemoteRoomInfoMsgType(visor, m)
		if err != nil {
			ms.errs <- err
			return
		}
	case message.TxtMsgType:
		//add the message to the visor and update repository
		visor.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*visor)
		if err != nil {
			ms.errs <- err
			return
		}
		//handle the message
		err := ms.handleRemoteRoomTextMsgType(m)
		if err != nil {
			ms.errs <- err
			return
		}
	case message.CmdMsgType:
		ms.errs <- fmt.Errorf("commands are not allowed from remote server chats")
		return
	case message.StatusMsgType:
		//handle message
		err := ms.handleRemoteStatusMsgType(m)
		if err != nil {
			ms.errs <- err
			return
		}
		return
	default:
		ms.errs <- fmt.Errorf("incorrect data received")
		return

	}
}

// handleRemoteRoomConnMsgType handles all messages of type ConnMsgtype of remote servers
func (ms MessengerService) handleRemoteRoomConnMsgType(m message.Message) error {
	ms.log.Debugln("handleRemoteRoomConnMsgType")

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
		n := notification.NewMsgNotification(m.Root)
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
		//notify that we send a reject message
		n := notification.NewMsgNotification(m.Root)
		err := ms.ns.Notify(n)
		if err != nil {
			return err
		}
		//? do we have to delete something here?
		//? maybe we don't even have to notify the user, that a rejection happened?
		return nil
	case message.ConnMsgTypeLeave:
		err = ms.removeOwnPkfromRoomForMessageFiltering(m)
		if err != nil {
			return err
		}
	case message.ConnMsgTypeDelete:
		//? do we have to delete something here? --> maybe the peer wants to save the chat history and not delete it, therefore we would have to add some kind of flag or so, that
		//? stops the peers from sending any messages to the deleted chat/server
	default:
		return fmt.Errorf("incorrect data received")
	}
	return nil

}

func (ms MessengerService) removeOwnPkfromRoomForMessageFiltering(m message.Message) error {
	visor, err := ms.visorRepo.GetByPK(m.GetRootVisor())
	if err != nil {
		return err
	}
	server, err := visor.GetServerByPK(m.GetRootServer())
	if err != nil {
		return err
	}
	room, err := server.GetRoomByPK(m.GetRootRoom())
	if err != nil {
		return err
	}
	err = room.DeleteMember(m.GetDestinationVisor())
	if err != nil {
		return err
	}
	err = server.SetRoom(*room)
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

	return nil
}

// handleRemoteRoomInfoMsgType handles messages of type info of peers
func (ms MessengerService) handleRemoteRoomInfoMsgType(v *chat.Visor, m message.Message) error {
	ms.log.Debugln("handleRemoteRoomInfoMsgType")

	pkroute := util.NewRoomRoute(m.GetRootVisor(), m.GetRootServer(), m.GetRootRoom())

	switch m.MsgSubtype {
	case message.InfoMsgTypeSingle:

		//unmarshal the received message bytes to info.Info
		i := info.Info{}
		err := json.Unmarshal(m.Message, &i)
		if err != nil {
			return err
		}

		ms.log.Debugln(i.PrettyPrint())

		err = v.SetRouteInfo(pkroute, i)
		if err != nil {
			return err
		}

		err = ms.visorRepo.Set(*v)
		if err != nil {
			return err
		}

		//notify about new info message
		n := notification.NewMsgNotification(pkroute)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
	case message.InfoMsgTypeRoomMembers:
		//unmarshal the received message bytes to map[cipher.Pubkey]peer.Peer
		members := map[cipher.PubKey]peer.Peer{}
		err := json.Unmarshal(m.Message, &members)
		if err != nil {
			return err
		}
		server, err := v.GetServerByPK(pkroute.Server)
		if err != nil {
			return err
		}
		room, err := server.GetRoomByPK(pkroute.Room)
		if err != nil {
			return err
		}

		//TODO: make method instead of direct access of variable
		room.Members = members

		err = server.SetRoom(*room)
		if err != nil {
			return err
		}

		err = v.SetServer(*server)
		if err != nil {
			return err
		}

		err = ms.visorRepo.Set(*v)
		if err != nil {
			return err
		}

		//notify about new info message
		n := notification.NewMsgNotification(pkroute)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
	case message.InfoMsgTypeRoomMuted:
		//unmarshal the received message bytes to map[cipher.Pubkey]bool
		muted := map[cipher.PubKey]bool{}
		err := json.Unmarshal(m.Message, &muted)
		if err != nil {
			return err
		}
		server, err := v.GetServerByPK(pkroute.Server)
		if err != nil {
			return err
		}
		room, err := server.GetRoomByPK(pkroute.Room)
		if err != nil {
			return err
		}

		//TODO: make method instead of direct access of variable
		room.Muted = muted

		err = server.SetRoom(*room)
		if err != nil {
			return err
		}

		err = v.SetServer(*server)
		if err != nil {
			return err
		}

		err = ms.visorRepo.Set(*v)
		if err != nil {
			return err
		}

		//notify about new info message
		n := notification.NewMsgNotification(pkroute)
		err = ms.ns.Notify(n)
		if err != nil {
			return err
		}
	}
	return nil
}

// handleRemoteRoomTextMsgType handles messages of type text of the remote chat
func (ms MessengerService) handleRemoteRoomTextMsgType(m message.Message) error {
	ms.log.Debugln("handleRemoteRoomTextMsgType")

	pkroute := util.NewRoomRoute(m.GetRootVisor(), m.GetRootServer(), m.GetRootRoom())

	ms.log.Debugln(m.PrettyPrintTextMessage())

	//notify about a new TextMessage
	n := notification.NewMsgNotification(pkroute)
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// handleRemoteStatusMsgType handles messages of type status of the remote chat
func (ms MessengerService) handleRemoteStatusMsgType(m message.Message) error {
	ms.log.Debugln("handleRemoteStatusMsgType")

	pkroute := m.Root

	v, err := ms.visorRepo.GetByPK(pkroute.Visor)
	if err != nil {
		return err
	}

	r, err := v.GetRoomByRoute(pkroute)
	if err != nil {
		return err
	}

	msg, err := r.GetMessageByID(string(m.Message))
	if err != nil {
		return err
	}

	msg.Status = m.MsgSubtype

	v.UpdateMessage(pkroute, msg)

	err = ms.visorRepo.Set(*v)
	if err != nil {
		return err
	}

	//notify about updated message
	//TODO: UpdateMsgNotification
	n := notification.NewMsgNotification(pkroute)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil

}
