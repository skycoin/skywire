// Package netcon contains code of the messenger of interfaceadapters
package netcon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
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

	ms.ns = ns
	ms.cliRepo = cR
	ms.usrRepo = uR
	ms.visorRepo = chR

	ms.errs = make(chan error, 1)

	return &ms
}

// readMessageLengthFromConnection reads a prefix message of the connection to get the length of the upcoming message
func readMessageLengthFromConnection(conn net.Conn) (*uint32, error) {
	prefixMessage := make([]byte, 4)
	_, err := io.ReadFull(conn, prefixMessage)
	if err != nil {
		return nil, err
	}
	messageLength := binary.BigEndian.Uint32(prefixMessage)
	return &messageLength, nil
}

func writeMessageLengthPrefixToConnection(message []byte, conn net.Conn) error {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(len(message)))
	fmt.Printf("Write prefix with %d Bytes to Conn: %s \n", len(prefix), conn.LocalAddr())
	_, err := conn.Write(prefix)
	if err != nil {
		return fmt.Errorf("failed to write prefix: %v ", err)
	}
	return nil
}

// readNBytesFromConnection reads n bytes from the given connection if a max. packetSize of 1024
func readNBytesFromConnection(n uint32, conn net.Conn) ([]byte, error) {
	packetBuffer := make([]byte, 1024)

	receivedBytes := make([]byte, 0)
	recievedBytesCounter := 0
	for recievedBytesCounter = 0; recievedBytesCounter < int(n); {
		packetSize, err := conn.Read(packetBuffer)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Read error - %s\n", err)
				return nil, err
			}
			break
		}
		receivedBytes = append(receivedBytes, packetBuffer[:packetSize]...)
		recievedBytesCounter += packetSize
		fmt.Printf("Data:	%d/%d	(PacketSize: %d) \n", recievedBytesCounter, n, packetSize)
	}
	fmt.Printf("Received %d bytes \n", recievedBytesCounter)

	return receivedBytes, nil
}

// HandleConnection handles the connection to the given Pubkey and incoming messages
func (ms MessengerService) HandleConnection(pk cipher.PubKey) {

	chatClient, err := ms.cliRepo.GetClient()
	if err != nil {
		ms.errs <- err
		return
	}

	connection, err := chatClient.GetConnByPK(pk)
	if err != nil {
		ms.errs <- err
		return
	}

	for {

		messageLength, err := readMessageLengthFromConnection(connection)
		if err != nil {
			ms.errs <- err
			continue
		}

		messageBytes, err := readNBytesFromConnection(*messageLength, connection)
		if err != nil {
			ms.errs <- err
			continue
		}

		receivedMessage, err := decodeReceivedBytesToMessage(messageBytes)
		if err != nil {
			ms.errs <- err
			continue
		}

		err = ms.handleReceivedMessage(*receivedMessage)
		if err != nil {
			ms.errs <- err
			continue
		}
	}
}

// handleReceivedMessage handles a received message
func (ms MessengerService) handleReceivedMessage(receivedMessage message.Message) error {
	chatClient, err := ms.cliRepo.GetClient()
	if err != nil {
		return err
	}

	localPK := chatClient.GetAppClient().Config().VisorPK

	if receivedMessage.IsFromRemoteP2PToLocalP2P(localPK) {
		go ms.handleP2PMessage(receivedMessage)
	} else if receivedMessage.IsFromRemoteServer() {
		go ms.handleRemoteServerMessage(receivedMessage)
	} else if receivedMessage.IsFromRemoteToLocalServer(localPK) {
		go ms.handleLocalServerMessage(receivedMessage)
	} else {
		return fmt.Errorf("received message that can't be matched to remote server, local server or p2p chat")
	}
	return nil
}

// decodeReceivedBytesToMessage decodes the given bytes to a message.Message
func decodeReceivedBytesToMessage(messageBytes []byte) (*message.Message, error) {
	receivedRawMessage := message.RAWMessage{}
	err := json.Unmarshal(messageBytes, &receivedRawMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal json message: %v ", err)
	}

	receivedMessage := message.NewMessage(receivedRawMessage)
	receivedMessage.FmtPrint(false)
	return &receivedMessage, nil
}

// DialPubKey dials the remote chat
func (ms MessengerService) DialPubKey(pk cipher.PubKey) (net.Conn, error) {

	chatClient, err := ms.cliRepo.GetClient()
	if err != nil {
		return nil, err
	}

	conn, err := chatClient.GetConnByPK(pk)
	if err == nil {
		return conn, nil
	}

	addr := appnet.Addr{
		Net:    chatClient.GetNetType(),
		PubKey: pk,
		Port:   chatClient.GetPort(),
	}

	var r = netutil.NewRetrier(chatClient.GetLog(), 50*time.Millisecond, netutil.DefaultMaxBackoff, 2, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ms.ctx = ctx
	err = r.Do(ms.ctx, func() error {
		//TODO: notify that dialing is happening, and even notify failed attempts?
		conn, err = chatClient.GetAppClient().Dial(addr)
		return err
	})
	if err != nil {
		return nil, err
	}

	err = chatClient.AddConn(pk, conn)
	if err != nil {
		return nil, err
	}

	err = ms.cliRepo.SetClient(*chatClient)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// sendMessageAndSaveItToDatabase sends a message and saves it to the database
func (ms MessengerService) sendMessageAndSaveItToDatabase(pkroute util.PKRoute, m message.Message) error {
	return ms.sendMessage(pkroute, m, true)
}

// sendMessageAndDontSaveItToDatabase sends a message but doesn't save it to the database
func (ms MessengerService) sendMessageAndDontSaveItToDatabase(pkroute util.PKRoute, m message.Message) error {
	return ms.sendMessage(pkroute, m, false)
}

// sendMessage sends a message to the given route
func (ms MessengerService) sendMessage(pkroute util.PKRoute, m message.Message, addToDatabase bool) error {

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

	chatClient, err := ms.cliRepo.GetClient()
	if err != nil {
		return err
	}

	m.FmtPrint(false)

	rm := message.NewRAWMessage(m)

	bytes, err := json.Marshal(rm)
	if err != nil {
		return fmt.Errorf("failed to marshal json: %v ", err)
	}

	conn, err := chatClient.GetConnByPK(m.Dest.Visor)
	if err != nil {
		conn, err = ms.DialPubKey(m.Dest.Visor)
		if err != nil {
			return err
		}
	}

	err = writeMessageLengthPrefixToConnection(bytes, conn)
	if err != nil {
		fmt.Printf("Failed to write message length")
	}

	fmt.Printf("Write %d Bytes to Conn: %s \n", len(bytes), conn.LocalAddr())
	_, err = conn.Write(bytes)
	if err != nil {
		return fmt.Errorf("failed to write bytes: %v", err)

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

// sendMessageToRemoteRoute sends the given message to a remote route (as p2p and client)
func (ms MessengerService) sendMessageToRemoteRoute(msg message.Message) error {
	//if the message goes to p2p we save it in database, if not we wait for the remote server to send us our message
	//this way we can see that the message was received by the remote server
	if msg.IsFromLocalToRemoteP2P() {
		err := ms.sendMessageAndSaveItToDatabase(msg.Dest, msg)
		if err != nil {
			return err
		}
	} else {
		err := ms.sendMessageAndDontSaveItToDatabase(msg.Dest, msg)
		if err != nil {
			return err
		}
	}

	n := notification.NewMsgNotification(msg.Dest, msg)
	err := ms.ns.Notify(n)
	if err != nil {
		return err
	}

	return nil
}

// sendMessageToLocalRoute "sends" the message to local server, so local server handles it, as it was sent from a remote route (used for messages send from server host, but as client)
func (ms MessengerService) sendMessageToLocalRoute(msg message.Message) error {
	go ms.handleLocalServerMessage(msg)

	return nil
}

// Listen is used to listen for new incoming connections and pass them to the HandleConnection routine
func (ms MessengerService) Listen() {
	chatClient, err := ms.cliRepo.GetClient()
	if err != nil {
		fmt.Printf("error getting client from repository: %s", err)
	}

	listener, err := chatClient.GetAppClient().Listen(chatClient.GetNetType(), chatClient.GetPort())
	if err != nil {
		fmt.Printf("Error listening network %v on port %d: %v\n", chatClient.GetNetType(), chatClient.GetPort(), err)
		return
	}

	chatClient.SetAppPort(chatClient.GetAppClient(), chatClient.GetPort())

	go func() {
		if err := <-ms.errs; err != nil {
			fmt.Printf("Error in go HandleConnection function: %s ", err)
		}
	}()

	for {
		fmt.Println("Accepting skychat conn...")
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Failed to accept conn:", err)
			return
		}
		fmt.Println("Accepted skychat conn")
		raddr := conn.RemoteAddr().(appnet.Addr)

		fmt.Printf("Accepted skychat conn on %s from %s\n", conn.LocalAddr(), raddr.PubKey)

		err = chatClient.AddConn(raddr.PubKey, conn)
		if err != nil {
			fmt.Println(err)
		}
		err = ms.cliRepo.SetClient(*chatClient)
		if err != nil {
			fmt.Println(err)
		}

		//error handling in anonymous go func above
		go ms.HandleConnection(raddr.PubKey)

	}
}
