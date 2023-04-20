// Package netcon contains code of the messenger of interfaceadapters
package netcon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
	"github.com/skycoin/skywire/pkg/app/appnet"
)

// MessengerService provides a netcon implementation of the Service
type MessengerService struct {
	ctx       context.Context
	ns        notification.Service
	cliRepo   client.Repository
	usrRepo   user.Repository
	visorRepo chat.Repository
	errs      chan error
}

// NewMessengerService constructor for MessengerService
func NewMessengerService(ns notification.Service, cR client.Repository, uR user.Repository, chR chat.Repository) *MessengerService {
	ms := MessengerService{}

	/*ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ms.ctx = ctx*/
	ms.ns = ns
	ms.cliRepo = cR
	ms.usrRepo = uR
	ms.visorRepo = chR

	ms.errs = make(chan error, 1)

	return &ms
}

// Handle handles the visor connection and incoming messages
func (ms MessengerService) Handle(pk cipher.PubKey) {
	errs := ms.errs

	pCli, err := ms.cliRepo.GetClient()
	if err != nil {
		errs <- err
		return
	}

	conn := pCli.GetConns()[pk]

	localPK := pCli.GetAppClient().Config().VisorPK

	for {

		//read packets
		buf := make([]byte, 32*1024*4)
		n, err := conn.Read(buf)
		fmt.Printf("Received %d bytes \n", n)
		if err != nil {
			fmt.Println("Failed to read packet:", err)
			//close and delete connection
			//? close connection ?
			err2 := pCli.DeleteConn(conn.RemoteAddr().(appnet.Addr).PubKey)
			if err2 != nil {
				errs <- err2
				return
			}
			errs <- err
			return
		}

		//unmarshal the received bytes to a message.Message
		m := message.RAWMessage{}
		fmt.Println("---------------------------------------------------------------------------------------------------")
		fmt.Println("New Raw Message")
		fmt.Println("---------------------------------------------------------------------------------------------------")

		err = json.Unmarshal(buf[:n], &m)
		if err != nil {
			fmt.Printf("Failed to unmarshal json message: %v \n", err)
		} else {
			jm := message.NewMessage(m)
			fmt.Println("---------------------------------------------------------------------------------------------------")
			fmt.Printf("Message: \n")
			fmt.Printf("ID: 		%d \n", jm.ID)
			fmt.Printf("Origin:		%s \n", jm.Origin)
			fmt.Printf("Time:		%s \n", jm.Time)
			fmt.Printf("Root:		%s \n", jm.Root.String())
			fmt.Printf("Dest:		%s \n", jm.Dest.String())
			fmt.Printf("MsgType:		%d \n", jm.MsgType)
			fmt.Printf("MsgSubType:		%d \n", jm.MsgSubtype)
			fmt.Printf("Message:		%s \n", string(jm.Message))
			fmt.Printf("Status:		%d \n", jm.Status)
			fmt.Printf("Seen:		%t \n", jm.Seen)
			fmt.Println("---------------------------------------------------------------------------------------------------")

			if jm.GetDestinationVisor() == localPK && jm.GetDestinationServer() == localPK && jm.GetDestinationRoom() == localPK && jm.GetRootVisor() == jm.GetRootServer() && jm.GetRootServer() == jm.GetRootRoom() {
				go ms.handleP2PMessage(jm)
			} else if jm.GetRootVisor() != jm.GetRootServer() && jm.GetRootServer() != jm.GetRootRoom() {
				go ms.handleRemoteServerMessage(jm)
			} else if jm.GetDestinationVisor() == localPK && jm.GetDestinationVisor() != jm.GetDestinationServer() && jm.GetDestinationServer() != jm.GetDestinationRoom() {
				go ms.handleLocalServerMessage(jm)
			} else {
				fmt.Println("received message that can't be matched to remote or local server or p2p chat")
			}
			if err != nil {
				fmt.Println(err)
				continue
			}
		}

	}
}

// Dial dials the remote chat
func (ms MessengerService) Dial(pk cipher.PubKey) (net.Conn, error) {

	pCli, err := ms.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("Error Getting client")
		return nil, err
	}

	conn, err := pCli.GetConnByPK(pk)
	if err == nil {
		//? is this necessary
		fmt.Printf("Connection already available, so delete old connection \n")
		err = conn.Close()
		if err != nil {
			return nil, err
		}
		err = pCli.DeleteConn(pk)
		if err != nil {
			return nil, err
		}
		//save client in repo
		err = ms.cliRepo.SetClient(*pCli)
		if err != nil {
			fmt.Println(err)
		}
	}

	addr := appnet.Addr{
		Net:    pCli.GetNetType(),
		PubKey: pk,
		Port:   pCli.GetPort(),
	}

	var r = netutil.NewRetrier(pCli.GetLog(), 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ms.ctx = ctx

	err = r.Do(ms.ctx, func() error {
		//? notify that dialing is happening?
		//? How about not deleting the visor, but saving the failed dials as a message so the user can see that something is happening.
		conn, err = pCli.GetAppClient().Dial(addr)
		//could not find a valid connection to a chat, so delete it.
		err2 := ms.visorRepo.Delete(pk)
		if err2 != nil {
			return err2
		}
		return err
	})

	if err != nil {
		return nil, err
	}

	//save connection in pCli
	err = pCli.AddConn(pk, conn)
	if err != nil {
		return nil, err
	}
	//save client in repo
	err = ms.cliRepo.SetClient(*pCli)
	if err != nil {
		fmt.Println(err)
	}

	return conn, nil
}

// sendMessage sends a message to the given route
func (ms MessengerService) sendMessage(pkroute util.PKRoute, m message.Message, addToDatabase bool) error {
	//check if visor exists
	v, err := ms.visorRepo.GetByPK(pkroute.Visor)
	if err != nil {
		var v2 chat.Visor
		//visor doesn't exist so we add a new remote route
		if pkroute.Visor == pkroute.Server { // --> P2P remote route
			v2 = chat.NewDefaultP2PVisor(pkroute.Visor)
		} else {
			v2 = chat.NewDefaultVisor(pkroute)
		}
		err2 := ms.visorRepo.Add(v2)
		if err2 != nil {
			return err2
		}
		v = &v2
		fmt.Printf("New skychat added: %s\n", pkroute.String())
	}

	// if the message is a p2p message we have to check if the p2p room exists in the server
	if pkroute.Visor == pkroute.Server {
		//maybe we already have a visor, but not yet a p2p-room so check if we have that.
		if v.P2PIsEmpty() {
			p2p := chat.NewDefaultP2PRoom(pkroute.Visor)
			err = v.AddP2P(p2p)
			if err != nil {
				return err
			}
			fmt.Printf("New P2P room added: %s\n", pkroute.String())
		}
	} else {
		// the message we want to send is a server / room message
		server, err := v.GetServerByPK(pkroute.Server)
		if err != nil {
			s := chat.NewDefaultServer(pkroute)
			err = v.AddServer(s)
			if err != nil {
				return err
			}
			fmt.Printf("New Server added: %s\n", pkroute.String())
		} else {
			//the server exists so we have to check if the room already exists
			_, err := server.GetRoomByPK(pkroute.Room)
			if err != nil {
				r := chat.NewDefaultRemoteRoom(pkroute)
				err = server.AddRoom(r)
				if err != nil {
					return err
				}
				err = v.SetServer(*server)
				if err != nil {
					return err
				}
				fmt.Printf("New Room added: %s\n", pkroute.String())
			}
		}

	}

	pCli, err := ms.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("Error Getting client")
		return err
	}

	fmt.Println("---------------------------------------------------------------------------------------------------")
	fmt.Println("Sending Message:")
	fmt.Printf("%s \n", pkroute.String())
	fmt.Println("---------------------------------------------------------------------------------------------------")
	fmt.Printf("Message: \n")
	fmt.Printf("ID: 		%d \n", m.ID)
	fmt.Printf("Origin:		%s \n", m.Origin)
	fmt.Printf("Time:		%s \n", m.Time)
	fmt.Printf("Root:		%s \n", m.Root.String())
	fmt.Printf("Dest:		%s \n", m.Dest.String())
	fmt.Printf("MsgType:		%d \n", m.MsgType)
	fmt.Printf("MsgSubType:		%d \n", m.MsgSubtype)
	fmt.Printf("Message:		%s \n", string(m.Message))
	fmt.Printf("Status:		%d \n", m.Status)
	fmt.Printf("Seen:		%t \n", m.Seen)
	fmt.Println("---------------------------------------------------------------------------------------------------")
	//fmt.Printf("Message Size: %T, %d\n", m, unsafe.Sizeof(m))
	fmt.Println("---------------------------------------------------------------------------------------------------")

	rm := message.NewRAWMessage(m)

	bytes, err := json.Marshal(rm)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	//Get connection from remote/destination visor of message
	conn, err := pCli.GetConnByPK(m.Dest.Visor)
	if err != nil {
		conn, err = ms.Dial(m.Dest.Visor)
		if err != nil {
			return err
		}
	}

	fmt.Printf("Write Bytes to Conn: %s \n", conn.LocalAddr())
	_, err = conn.Write(bytes)
	if err != nil {
		return err
	}

	if addToDatabase {
		v.AddMessage(pkroute, m)
		err = ms.visorRepo.Set(*v)
		if err != nil {
			return err
		}
	}

	return nil
}

// sendMessageNirvana sends a message to the given route without saving it somewhere
func (ms MessengerService) sendMessageNirvana(route util.PKRoute, m message.Message) error {

	pCli, err := ms.cliRepo.GetClient()
	if err != nil {
		return err
	}

	m.Dest = route

	bytes, err := json.Marshal(m)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	conn := pCli.GetConns()[route.Visor]
	if conn == nil {
		conn, err = ms.Dial(route.Visor)
		if err != nil {
			return err
		}
	}

	_, err = conn.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}

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

	err = ms.sendMessage(route, m, true)
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

// sendMessageToRemoteRoute sends the given message to a remote route (as p2p and client)
func (ms MessengerService) sendMessageToRemoteRoute(msg message.Message) error {
	//if the message goes to p2p we save it in database, if not we wait for the remote server to send us our message
	//this way we can see that the message was received by the remote server
	saveInDatabase := (msg.Dest.Visor == msg.Dest.Server)
	err := ms.sendMessage(msg.Dest, msg, saveInDatabase)
	if err != nil {
		return err
	}

	//notify about sent text message
	n := notification.NewMsgNotification(msg.Dest, msg)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// sendMessageToLocalRoute "sends" the message to local server, so local server handles it, as it was sent from a remote route (used for messages send from server host, but as client)
func (ms MessengerService) sendMessageToLocalRoute(msg message.Message) error {
	go ms.handleLocalServerMessage(msg)
	//notification is handled inside handleServerMessage

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
	err := ms.sendMessage(pkroute, m, true)
	if err != nil {
		return err
	}
	return nil
}

// SendChatRejectMessage sends an reject-message from the root to the destination
func (ms MessengerService) SendChatRejectMessage(root util.PKRoute, dest util.PKRoute) error {
	m := message.NewChatRejectMessage(root, dest)
	err := ms.sendMessageNirvana(dest, m)
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

	root := util.NewVisorOnlyRoute(usr.GetInfo().GetPK())

	m := message.NewChatLeaveMessage(root, pkroute)
	err = ms.sendMessage(pkroute, m, true)
	if err != nil {
		return err
	}
	return nil
}

// Listen is used to listen for new incoming connections and pass them to the connection_handle routine
func (ms MessengerService) Listen() {
	pCli, err := ms.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("Error getting client from repository: %s", err)
	}

	l, err := pCli.GetAppClient().Listen(pCli.GetNetType(), pCli.GetPort())
	if err != nil {
		fmt.Printf("Error listening network %v on port %d: %v\n", pCli.GetNetType(), pCli.GetPort(), err)
		return
	}

	pCli.SetAppPort(pCli.GetAppClient(), pCli.GetPort())

	go func() {
		if err := <-ms.errs; err != nil {
			fmt.Printf("Error in go handle function: %s ", err)
		}
	}()

	for {
		fmt.Println("Accepting skychat conn...")
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Failed to accept conn:", err)
			return
		}
		fmt.Println("Accepted skychat conn")
		raddr := conn.RemoteAddr().(appnet.Addr)

		fmt.Printf("Accepted skychat conn on %s from %s\n", conn.LocalAddr(), raddr.PubKey)

		//add connection to active connections of client
		err = pCli.AddConn(raddr.PubKey, conn)
		if err != nil {
			fmt.Println(err)
		}
		err = ms.cliRepo.SetClient(*pCli)
		if err != nil {
			fmt.Println(err)
		}

		//error handling in anonymous go func above
		go ms.Handle(raddr.PubKey)

	}
}
