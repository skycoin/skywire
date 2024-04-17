package messengerimpl

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// SendRouteRequestMessage sends a request message to join the specified route
// if route.Visor == route.Server == route.Room -> P2P request
// if route.Visor + route.Server == route.Room -> ServerJoinRequest
// if route.Visor + route.Server + route.Room -> RoomRequest
func (ms MessengerService) SendRouteRequestMessage(route util.PKRoute) error {

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	m := message.NewRouteRequestMessage(usr.GetInfo().GetPK(), route)

	err = ms.sendMessageAndSaveItToDatabase(route, m)
	if err != nil {
		return err
	}

	//notify about the added route
	an := notification.NewAddRouteNotification(route)
	err = ms.ns.Notify(an)
	if err != nil {
		return err
	}
	return nil
}

// SendTextMessage sends a text message to the given route
func (ms MessengerService) SendTextMessage(route util.PKRoute, msg []byte) error {

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	m := message.NewTextMessage(usr.GetInfo().GetPK(), route, msg)

	if m.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(m)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(m)
		if err != nil {
			return err
		}
	}

	return nil
}

// SendAddRoomMessage sends a command message to add a room to the given route
func (ms MessengerService) SendAddRoomMessage(route util.PKRoute, info info.Info) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	bytes, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	m := message.NewAddRoomMessage(root, route, bytes)

	if m.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(m)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(m)
		if err != nil {
			return err
		}
	}

	return nil
}

// SendDeleteRoomMessage sends a command message to delete a room of the given route
func (ms MessengerService) SendDeleteRoomMessage(route util.PKRoute) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	m := message.NewDeleteRoomMessage(root, route)

	if m.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(m)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(m)
		if err != nil {
			return err
		}
	}

	return nil
}

// SendMutePeerMessage sends a command message to mute a peer in the given route
func (ms MessengerService) SendMutePeerMessage(pkroute util.PKRoute, pk cipher.PubKey) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	bytes, err := json.Marshal(pk)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	msg := message.NewMutePeerMessage(root, pkroute, bytes)

	if msg.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(msg)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendUnmutePeerMessage sends a command message to unmute a peer in the given route
func (ms MessengerService) SendUnmutePeerMessage(pkroute util.PKRoute, pk cipher.PubKey) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	bytes, err := json.Marshal(pk)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	msg := message.NewUnmutePeerMessage(root, pkroute, bytes)

	if msg.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(msg)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendHireModeratorMessage sends a command message to hire a peer as moderator
func (ms MessengerService) SendHireModeratorMessage(pkroute util.PKRoute, pk cipher.PubKey) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	bytes, err := json.Marshal(pk)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	msg := message.NewHireModeratorMessage(root, pkroute, bytes)

	if msg.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(msg)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendFireModeratorMessage sends a command message to fire a moderator
func (ms MessengerService) SendFireModeratorMessage(pkroute util.PKRoute, pk cipher.PubKey) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	bytes, err := json.Marshal(pk)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	msg := message.NewFireModeratorMessage(root, pkroute, bytes)

	if msg.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(msg)
		if err != nil {
			return err
		}
	} else {
		err = ms.sendMessageToRemoteRoute(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

// SendInfoMessage sends an info message to the given chat and notifies about sent message
func (ms MessengerService) SendInfoMessage(pkroute util.PKRoute, root util.PKRoute, dest util.PKRoute, info info.Info) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	bytes, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
		return err
	}

	m := message.NewChatInfoMessage(root, dest, bytes)

	//send info to local route (we as host are a member also and have to update our info inside the server)
	if m.Dest.Visor == usr.GetInfo().GetPK() {
		err = ms.sendMessageToLocalRoute(m)
		if err != nil {
			return err
		}
	} else if pkroute.Visor != usr.GetInfo().GetPK() {
		//Send info to a remote route (as peer and as client)
		err = ms.sendMessageToRemoteRoute(m)
		if err != nil {
			return err
		}
	}

	return nil

}

// SendChatAcceptMessage sends an accept-message from the root to the destination
func (ms MessengerService) SendChatAcceptMessage(pkroute util.PKRoute, root util.PKRoute, dest util.PKRoute) error {
	m := message.NewChatAcceptMessage(root, dest)
	err := ms.sendMessageAndSaveItToDatabase(pkroute, m)
	if err != nil {
		return err
	}
	return nil
}

// SendChatRejectMessage sends an reject-message from the root to the destination
func (ms MessengerService) SendChatRejectMessage(root util.PKRoute, dest util.PKRoute) error {
	m := message.NewChatRejectMessage(root, dest)
	err := ms.sendMessageAndDontSaveItToDatabase(dest, m)
	if err != nil {
		return err
	}
	return nil
}

// SendLeaveRouteMessage sends a leave-message from the root to the destination
func (ms MessengerService) SendLeaveRouteMessage(pkroute util.PKRoute) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	root := util.NewP2PRoute(usr.GetInfo().GetPK())

	m := message.NewChatLeaveMessage(root, pkroute)
	err = ms.sendMessageAndDontSaveItToDatabase(pkroute, m)
	if err != nil {
		return err
	}
	return nil
}

// SendMessageReceived sends a message to let a peer know that his message was received correctly
func (ms MessengerService) SendMessageReceived(msg message.Message) error {

	m := message.NewStatusMessage(msg.Dest.Visor, msg.Dest, msg.Root, msg.ID, message.MsgStatusReceived)

	err := ms.sendMessageAndDontSaveItToDatabase(msg.Root, m)
	if err != nil {
		return err
	}
	return nil
}
