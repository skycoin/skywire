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
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println("Failed to read packet:", err)
			//close and delete connection
			//TODO: close connection
			err2 := pCli.DeleteConn(conn.RemoteAddr().(appnet.Addr).PubKey)
			if err2 != nil {
				errs <- err2
				return
			}
			errs <- err
			return
		}

		//unmarshal the received bytes to a message.Message
		m := message.Message{}

		err = json.Unmarshal(buf[:n], &m)
		if err != nil {
			fmt.Printf("Failed to unmarshal json message: %v", err)
		} else {
			//unmarshal the received bytes to a message.JSONMessage{}
			buf := make([]byte, 32*1024)
			n, err := conn.Read(buf)
			if err != nil {
				fmt.Println("Failed to read packet:", err)
				continue
			}
			jm := message.JSONMessage{}
			err = json.Unmarshal(buf[:n], &jm)
			if err != nil {
				fmt.Printf("Failed to unmarshal json jsonMessage: %v", err)
				continue
			}
			m = message.NewMessage(jm)

			//handle messages in dependency of destination
			if m.GetDestinationVisor() == localPK {
				if m.GetDestinationServer() == localPK {
					err = ms.handleP2PMessage(m)
				} else {
					err = ms.handleLocalServerMessage(m)
				}
				if err != nil {
					fmt.Println(err)
					continue
				}
			} else {
				err = ms.handleRemoteServerMessage(m)
				if err != nil {
					fmt.Println(err)
					continue
				}

			}
		}

	}
}

//TODO: think about how to handle this
//If the received message has status MsgSTatusInitial it was just send and we have to send it back to let the
//peer know that we received it.
//if m.GetStatus() == message.MsgStatusInitial {
//	m.SetStatus(message.MsgStatusReceived)
//	ms.sendMessage(pk, m)
//}
//notify that a new message has been received
//save message to chatrepo
//TODO: make NewReceivedTextMessage
//! Can also be a message of the remote that the own send message has been received

// Dial dials the remote chat
func (ms MessengerService) Dial(pk cipher.PubKey) (net.Conn, error) {

	c, err := ms.visorRepo.GetByPK(pk)
	if err != nil {
		//Should not happen as before dial chats get added to repo
		return nil, err
	}

	pCli, err := ms.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("Error Getting client")
		return nil, err
	}

	conn := c.GetConnection()
	/*if conn != nil {
		//TODO: maybe delete old connection and reconnect?
		//TODO: or skip dialing if connection alive
		//pCli.GetAppClient().Close()

	}*/

	addr := appnet.Addr{
		Net:    pCli.GetNetType(),
		PubKey: c.GetPK(),
		Port:   pCli.GetPort(),
	}

	var r = netutil.NewRetrier(pCli.GetLog(), 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ms.ctx = ctx

	err = r.Do(ms.ctx, func() error {
		//TODO: notify that dialing is happening?
		conn, err = pCli.GetAppClient().Dial(addr)
		//could not find a valid connection to a chat, so delete it.
		err2 := ms.visorRepo.Delete(c.PK)
		if err2 != nil {
			return err2
		}
		return err
	})

	if err != nil {
		return nil, err
	}

	c.SetConnection(conn)
	err = ms.visorRepo.Set(*c)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// sendMessage sends a message to the given route
func (ms MessengerService) sendMessage(dest util.PKRoute, m message.Message) error {
	v, err := ms.visorRepo.GetByPK(dest.Visor)
	if err != nil {
		ch := chat.NewUndefinedVisor(dest.Visor)
		err2 := ms.visorRepo.Add(ch)
		if err2 != nil {
			return err2
		}
		v = &ch
		fmt.Printf("New skychat added: %s\n", dest.Visor)
	}

	m.Dest = dest

	bytes, err := json.Marshal(m)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	conn := v.GetConnection()
	if conn == nil {
		conn, err = ms.Dial(dest.Visor)
		if err != nil {
			return err
		}
	}

	_, err = conn.Write(bytes)
	if err != nil {
		return err
	}

	v, err = ms.visorRepo.GetByPK(dest.Visor)
	if err != nil {
		return err
	}
	v.AddMessage(m)
	err = ms.visorRepo.Set(*v)
	if err != nil {
		return err
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
// if route.Visor == route.Server -> P2P request
// if route.Visor + (route.Server == nil) -> P2P request
// if route.Visor + route.Server -> ServerJoinRequest
// if route.Visor + route.Server + route.Room -> RoomRequest
func (ms MessengerService) SendRouteRequestMessage(route util.PKRoute) error {

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	m := message.NewRouteRequestMessage(usr.GetInfo().GetPK(), route)

	err = ms.sendMessage(route, m)
	if err != nil {
		return err
	}

	//TODO: think about putting this notification in add_chat use case
	//notify about the added route
	an := notification.NewAddRouteNotification(route)
	err = ms.ns.Notify(an)
	if err != nil {
		return err
	}

	//notify about sent chat request message
	n := notification.NewMsgNotification(route, m)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// SendTextMessage sends a text message to the given route
//[]: root and destination!!
func (ms MessengerService) SendTextMessage(route util.PKRoute, msg []byte) error {
	fmt.Println("MessengerService - SendTextMessage")

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

// sendMessageToRemoteRoute sends the given message to a remote route
func (ms MessengerService) sendMessageToRemoteRoute(msg message.Message) error {
	err := ms.sendMessage(msg.Dest, msg)
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

// sendMessageToLocalRoute handles the message as a message received from a server
func (ms MessengerService) sendMessageToLocalRoute(msg message.Message) error {
	err := ms.handleLocalServerMessage(msg)
	if err != nil {
		return err
	}
	//notification is handled inside handleServerMessage

	return nil
}

// SendInfoMessage sends an info message to the given chat and notifies about sent message
func (ms MessengerService) SendInfoMessage(root util.PKRoute, dest util.PKRoute, info info.Info) error {
	bytes, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	m := message.NewChatInfoMessage(root, dest, bytes)

	err = ms.sendMessage(dest, m)
	if err != nil {
		return err
	}

	//notify about sent info message
	n := notification.NewMsgNotification(dest, m)
	err = ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// SendChatAcceptMessage sends an accept-message from the root to the destination
func (ms MessengerService) SendChatAcceptMessage(root util.PKRoute, dest util.PKRoute) error {
	m := message.NewChatAcceptMessage(root, dest)
	err := ms.sendMessage(dest, m)
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

		go ms.Handle(raddr.PubKey)

		if err := <-ms.errs; err != nil {
			fmt.Printf("Error in go handle function: %s ", err)
		}

	}
}
