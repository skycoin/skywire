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
	"github.com/skycoin/skywire/pkg/app/appnet"
)

// MessengerService provides a netcon implementation of the Service
type MessengerService struct {
	ctx      context.Context
	ns       notification.Service
	cliRepo  client.Repository
	usrRepo  user.Repository
	chatRepo chat.Repository
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
	ms.chatRepo = chR

	return &ms
}

// Handle handles the chat connection and incoming messanges
func (ms MessengerService) Handle(pk cipher.PubKey) error {
	var err error

	c, err := ms.chatRepo.GetByPK(pk)
	if err != nil {
		ch := chat.NewUndefinedChat(pk)
		ms.chatRepo.Add(ch) //nolint:errcheck
		c = &ch
		fmt.Printf("New skychat added: %s\n", pk)
	}

	conn := c.GetConnection()
	if conn == nil {
		fmt.Printf("Error, no connection\n")
		conn, err = ms.Dial(pk)
		if err != nil {
			return err
		}
	}

	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println("Failed to read packet:", err)
			c.DeleteConnection()
			return err
		}

		//unmarshal the received bytes to a message.Message
		m := message.Message{}

		err = json.Unmarshal(buf[:n], &m)
		if err != nil {
			fmt.Printf("Failed to unmarshal json message: %v", err)
		}

		//TODO: first message has to be a request message, if not block and delete
		//--> if we don't handle this it would be possible to send something else than request message via another
		// app and bypass the blacklist

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

		//get the current chat so when updating nothing gets overwritten
		c, _ := ms.chatRepo.GetByPK(pk)

		switch m.GetMessageType() {
		case message.ConnMsgType:
			c.AddMessage(m)
			ms.chatRepo.Update(*c)
			err := ms.handleConnMsgType(m)
			if err != nil {
				fmt.Println(err)
			}
		case message.InfoMsgType:
			jm := message.JSONMessage{}
			err = json.Unmarshal(buf[:n], &jm)
			if err != nil {
				fmt.Printf("Failed to unmarshal json message: %v", err)
			}
			m = message.NewMessage(jm)
			c.AddMessage(m)
			ms.chatRepo.Update(*c)

			err = ms.handleInfoMsgType(c, m)
			if err != nil {
				fmt.Println(err)
			}
		case message.TxtMsgType:
			jm := message.JSONMessage{}
			err = json.Unmarshal(buf[:n], &jm)
			if err != nil {
				fmt.Printf("Failed to unmarshal json message: %v", err)
			}
			m = message.NewMessage(jm)
			c.AddMessage(m)
			ms.chatRepo.Update(*c)
			err := ms.handleTextMsgType(c, m)
			if err != nil {
				fmt.Println(err)
			}
		case message.CmdMsgType:
			//not allowed, only for servers/groups
		default:
			fmt.Printf("Incorrect data received")
		}
	}
}

// handleConnMsgType handles an incoming connection message and either accepts it and sends back the own info as message
// or if the public key is in the blacklist rejects the chat request.
func (ms MessengerService) handleConnMsgType(m message.Message) error {
	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	switch m.MsgSubtype {
	case message.ConnMsgTypeRequest:
		//check if sender is in blacklist, if not so send info message back, else reject chat
		if !usr.GetSettings().InBlacklist(m.Sender) {
			//notify about the new chat initiated by the user
			an := notification.NewChatNotification(m.Sender)
			ms.ns.Notify(an)

			msg := message.NewChatAcceptMessage(usr.GetInfo().GetPK())
			ms.sendMessage(m.Sender, msg)

			ms.SendInfoMessage(m.Sender, *usr.GetInfo())

			//n := notification.NewMsgNotification(m.Sender, m)
			//ms.ns.Notify(n)
		} else {
			msg := message.NewChatRejectMessage(usr.GetInfo().GetPK())
			ms.sendMessage(m.Sender, msg)
			ms.chatRepo.Delete(m.Sender)

			//TODO: notify about a rejected chat request cause of blacklist
			return fmt.Errorf("pk in blacklist")
		}
	case message.ConnMsgTypeAccept:
		ms.SendInfoMessage(m.Sender, *usr.GetInfo())

	case message.ConnMsgTypeReject:
		//ms.chatRepo.Delete(m.Sender)

		n := notification.NewMsgNotification(m.Sender, m)
		ms.ns.Notify(n)
		return fmt.Errorf("peer rejected chat")

	}

	return nil
}

//TODO: document what function does
func (ms MessengerService) handleInfoMsgType(c *chat.Chat, m message.Message) error {
	//save info in chat info
	//unmarshal the received message bytes to info.Info
	i := info.Info{}
	err := json.Unmarshal(m.Message, &i)
	if err != nil {
		fmt.Printf("Failed to unmarshal json message: %v", err)
	}
	c.Info = i
	ms.chatRepo.Update(*c)

	//notify about new info message
	n := notification.NewMsgNotification(c.GetPK(), m)
	ms.ns.Notify(n)

	return nil
}

//TODO: document what function does
func (ms MessengerService) handleTextMsgType(c *chat.Chat, m message.Message) error {

	n := notification.NewMsgNotification(c.GetPK(), message.NewTextMessage(m.Sender, m.Message))
	ms.ns.Notify(n)

	return nil
}

// Dial dials the remote chat
func (ms MessengerService) Dial(pk cipher.PubKey) (net.Conn, error) {

	c, err := ms.chatRepo.GetByPK(pk)
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
	if conn != nil {
		//TODO: maybe delete old connection and reconnect?
		//TODO: or skip dialing if connection alive
		//pCli.GetAppClient().Close()
	}

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
		ms.chatRepo.Delete(c.PK)
		return err
	})

	if err != nil {
		return nil, err
	}

	c.SetConnection(conn)
	ms.chatRepo.Update(*c)
	return conn, nil
}

// sendMessage sends a message to the given chat
func (ms MessengerService) sendMessage(pk cipher.PubKey, m message.Message) error {
	c, err := ms.chatRepo.GetByPK(pk)
	if err != nil {
		ch := chat.NewUndefinedChat(pk)
		ms.chatRepo.Add(ch)
		c = &ch
		fmt.Printf("New skychat added: %s\n", pk)
	}

	bytes, err := json.Marshal(m)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	conn := c.GetConnection()
	if conn == nil {
		conn, err = ms.Dial(pk)
		if err != nil {
			return err
		}
	}

	_, err = conn.Write(bytes)
	if err != nil {
		return err
	}

	c, _ = ms.chatRepo.GetByPK(pk)
	c.AddMessage(m)
	ms.chatRepo.Update(*c)

	return nil
}

// SendChatRequestMessage sends a chat request message to request a chat
func (ms MessengerService) SendChatRequestMessage(pk cipher.PubKey) error {

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	m := message.NewChatRequestMessage(usr.GetInfo().GetPK())

	err = ms.sendMessage(pk, m)
	if err != nil {
		return err
	}

	//TODO: think about putting this notification in add_chat use case
	//notify about the added chat
	an := notification.NewAddChatNotification(pk)
	ms.ns.Notify(an)

	//notify about sent chat request message
	n := notification.NewMsgNotification(pk, m)
	ms.ns.Notify(n)

	return nil
}

// SendTextMessage sends a text message to the given chat
func (ms MessengerService) SendTextMessage(pk cipher.PubKey, msg []byte) error {
	fmt.Println("MessengerService - SendTextMessage")

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	m := message.NewTextMessage(usr.GetInfo().GetPK(), msg)

	err = ms.sendMessage(pk, m)
	if err != nil {
		return err
	}

	//notify about sent text message
	n := notification.NewMsgNotification(pk, m)
	ms.ns.Notify(n)

	return nil
}

// SendInfoMessage sends a info message to the given chat
func (ms MessengerService) SendInfoMessage(pk cipher.PubKey, info info.Info) error {
	fmt.Println("MessengerService - SendInfoMessage")

	bytes, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	usr, err := ms.usrRepo.GetUser()
	if err != nil {
		fmt.Printf("Error getting user from repository: %s", err)
		return err
	}

	m := message.NewChatInfoMessage(usr.GetInfo().GetPK(), bytes)

	err = ms.sendMessage(pk, m)
	if err != nil {
		return err
	}

	//notify about sent info message
	n := notification.NewMsgNotification(pk, m)
	ms.ns.Notify(n)

	return nil
}

// Listen is used to listen for new incoming chats and pass them to the connection_handle routine
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

		//check if the remote addr already is a saved chat
		c, err := ms.chatRepo.GetByPK(raddr.PubKey)
		if err != nil {
			ch := chat.NewUndefinedChat(raddr.PubKey)
			ms.chatRepo.Add(ch)
			c = &ch
			fmt.Printf("New skychat added: %s\n", raddr.PubKey)
		}
		c.SetConnection(conn)
		ms.chatRepo.Update(*c)

		go ms.Handle(raddr.PubKey)
	}
}
