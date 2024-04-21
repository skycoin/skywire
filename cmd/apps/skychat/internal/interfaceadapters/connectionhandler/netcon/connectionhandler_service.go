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

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
	"github.com/skycoin/skywire/pkg/app/appnet"
)

// ConnectionHandlerService provides a netcon implementation of the Service
type ConnectionHandlerService struct {
	ctx          context.Context
	log          *logging.Logger
	ns           notification.Service
	cliRepo      client.Repository
	visorRepo    chat.Repository
	msgrx        chan message.Message // out-channel for this servie (when the connection received a message and wants to send it to other services)
	errs         chan error
	conns        map[cipher.PubKey]net.Conn
	handledConns map[cipher.PubKey]net.Conn
}

// NewConnectionHandlerService constructor for ConnectionHandlerService
func NewConnectionHandlerService(ns notification.Service, cR client.Repository, chR chat.Repository, msgrx chan message.Message) *ConnectionHandlerService {
	ch := ConnectionHandlerService{}

	ch.log = logging.MustGetLogger("chat:connhandler")

	ch.ns = ns
	ch.cliRepo = cR
	ch.visorRepo = chR

	ch.msgrx = msgrx

	ch.conns = make(map[cipher.PubKey]net.Conn)
	ch.errs = make(chan error, 1)

	return &ch
}

// HandleConnection handles the connection to the given Pubkey and incoming messages
func (ch ConnectionHandlerService) HandleConnection(pk cipher.PubKey) {

	connection, err := ch.GetConnByPK(pk)
	if err != nil {
		ch.errs <- err
		return
	}

	if ch.ConnectionToPkHandled(pk) {
		ch.errs <- fmt.Errorf("connection already handled")
		return
	}

	err = ch.AddConnToHandled(pk, connection)
	if err != nil {
		ch.errs <- err
		return
	}

	for {

		messageLength, err := readMessageLengthFromConnection(connection)
		if err != nil {
			ch.errs <- err
			continue
		}

		messageBytes, err := readNBytesFromConnection(*messageLength, connection)
		if err != nil {
			ch.errs <- err
			continue
		}

		receivedMessage, err := decodeReceivedBytesToMessage(messageBytes)
		if err != nil {
			ch.errs <- err
			continue
		}

		ch.log.Debugln(receivedMessage.PrettyPrint(false))

		ch.msgrx <- *receivedMessage

	}

}

// GetReceiveChannel returns the channel used to 'broadcast' received messages from the connectionhandler
func (ch ConnectionHandlerService) GetReceiveChannel() chan message.Message {
	return ch.msgrx
}

// readMessageLengthFromConnection reads a prefix message of the connection to get the length of the upcoming message
func readMessageLengthFromConnection(conn net.Conn) (*uint32, error) {
	prefixMessage := make([]byte, 4)
	_, err := io.ReadFull(conn, prefixMessage)
	if err != nil {
		return nil, err
	}
	messageLength := binary.BigEndian.Uint32(prefixMessage)
	fmt.Printf("readMessageLengthFromConnection - Message Length:	%d \n", messageLength)
	return &messageLength, nil
}

func writeMessageLengthPrefixToConnection(message []byte, conn net.Conn) error {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(len(message)))
	fmt.Printf("Write prefix with %d Bytes to Conn: %s \n", len(prefix), conn.LocalAddr())
	_, err := conn.Write(prefix)
	if err != nil {
		return fmt.Errorf("failed to write prefix: %v", err)
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
		fmt.Printf("Data:	%d/%d		(PacketSize: %d) \n", recievedBytesCounter, n, packetSize)
	}
	fmt.Printf("Received %d bytes \n", recievedBytesCounter)

	return receivedBytes, nil
}

// decodeReceivedBytesToMessage decodes the given bytes to a message.Message
func decodeReceivedBytesToMessage(messageBytes []byte) (*message.Message, error) {
	receivedRawMessage := message.RAWMessage{}
	err := json.Unmarshal(messageBytes, &receivedRawMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal json message: %v ", err)
	}

	receivedMessage := message.NewMessage(receivedRawMessage)

	return &receivedMessage, nil
}

// DialPubKey dials the remote chat
func (ch ConnectionHandlerService) DialPubKey(pk cipher.PubKey) (net.Conn, error) {

	chatClient, err := ch.cliRepo.GetClient()
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
	ch.ctx = ctx
	err = r.Do(ch.ctx, func() error {
		//TODO: notify that dialing is happening, and even notify failed attempts?
		conn, err = chatClient.GetAppClient().Dial(addr)
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func addP2PIfEmpty(v *chat.Visor) error {
	if v.P2PIsEmpty() {
		p2p := chat.NewDefaultP2PRoom(v.GetPK())
		err := v.AddP2P(p2p)
		if err != nil {
			return err
		}
		fmt.Printf("New P2P room added to visor %s\n", v.GetPK().String())
	}
	return nil
}

// SendMessage sends a message to the given route
//
// if addToDatabase is true the message will be saved locally, otherwise not.
// Attention: a destination pkroute can also be a local destination so m.destination and pkroute can differ
func (ch ConnectionHandlerService) SendMessage(pkroute util.PKRoute, m message.Message, addToDatabase bool) error {

	v, err := ch.getVisorAndSetupIfNecessary(pkroute)
	if err != nil {
		return err
	}

	ch.log.Debugln(m.PrettyPrint(false))

	rm := message.NewRAWMessage(m)

	bytes, err := json.Marshal(rm)
	if err != nil {
		return fmt.Errorf("failed to marshal json: %v ", err)
	}

	conn, err := ch.GetConnByPK(m.Dest.Visor)
	if err != nil {
		conn, err = ch.DialPubKey(m.Dest.Visor)
		if err != nil {
			return err
		}
		err = ch.AddConn(pkroute.Visor, conn)
		if err != nil {
			return err
		}
		ch.log.Debugf("added conn %s	%s\n", conn.RemoteAddr().String(), conn.RemoteAddr().Network())

		go ch.HandleConnection(pkroute.Visor) //nolint:errcheck
	}

	err = writeMessageLengthPrefixToConnection(bytes, conn)
	if err != nil {
		ch.log.Errorln("Failed to write message length")
	}

	fmt.Printf("Write %d Bytes to Conn: %s \n", len(bytes), conn.LocalAddr())
	_, err = conn.Write(bytes)
	if err != nil {
		return fmt.Errorf("failed to write bytes: %v", err)

	}

	if addToDatabase {
		m.Status = message.MsgStatusSent
		v.AddMessage(pkroute, m)
		err = ch.visorRepo.Set(*v)
		if err != nil {
			return err
		}
	}
	return nil
}

func (ch ConnectionHandlerService) getVisorAndSetupIfNecessary(pkroute util.PKRoute) (*chat.Visor, error) {
	v, err := ch.getExistingVisorOrAddNewIfNotExists(pkroute)
	if err != nil {
		return nil, err
	}

	if pkroute.IsP2PRoute() {
		err = addP2PIfEmpty(v)
		if err != nil {
			return nil, err
		}
	} else {
		server, err := v.GetServerByRouteOrAddNewIfNotExists(pkroute)
		if err != nil {
			return nil, err
		}

		_, err = server.GetRoomByRouteOrAddNewIfNotExists(pkroute)
		if err != nil {
			return nil, err
		}

		err = v.SetServer(*server)
		if err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (ch ConnectionHandlerService) getExistingVisorOrAddNewIfNotExists(pkroute util.PKRoute) (*chat.Visor, error) {

	if ch.visorExists(pkroute) {
		return ch.visorRepo.GetByPK(pkroute.Visor)
	}

	var v chat.Visor

	if pkroute.IsP2PRoute() {
		v = chat.NewDefaultP2PVisor(pkroute.Visor)
	} else {
		v = chat.NewDefaultVisor(pkroute)
	}

	err := ch.visorRepo.Add(v)
	if err != nil {
		return nil, err
	}
	return &v, nil

}

func (ch ConnectionHandlerService) visorExists(pkroute util.PKRoute) bool {
	_, err := ch.visorRepo.GetByPK(pkroute.Visor)
	return err == nil
}

// Listen is used to listen for new incoming connections and pass them to the HandleConnection routine
func (ch ConnectionHandlerService) Listen() {
	chatClient, err := ch.cliRepo.GetClient()
	if err != nil {
		ch.log.Errorf("error getting client from repository: %s", err)
	}

	listener, err := chatClient.GetAppClient().Listen(chatClient.GetNetType(), chatClient.GetPort())
	if err != nil {
		ch.log.Errorf("Error listening network %v on port %d: %v\n", chatClient.GetNetType(), chatClient.GetPort(), err)
		return
	}

	chatClient.SetAppPort(chatClient.GetAppClient(), chatClient.GetPort())

	go func() {
		if err := <-ch.errs; err != nil {
			ch.log.Errorf("Error in go HandleConnection function: %s \n", err)
		}
	}()

	for {
		ch.log.Debugln("Accepting skychat conn...")
		conn, err := listener.Accept()
		if err != nil {
			ch.log.Errorln("Failed to accept conn:", err)
			return
		}
		raddr := conn.RemoteAddr().(appnet.Addr)

		ch.log.Debugf("Accepted skychat conn on %s from %s\n", conn.LocalAddr(), raddr.PubKey)

		err = ch.AddConn(raddr.PubKey, conn)
		if err != nil {
			ch.log.Error(err)
		}

		//error handling in anonymous go func above
		go ch.HandleConnection(raddr.PubKey)
		defer func() {
			err = ch.DeleteConnFromHandled(raddr.PubKey)
			ch.log.Error(err.Error())
		}()

	}
}

// GetConnByPK returns the conn of the given visor pk or an error if there is no open connection to the requested visor
func (ch *ConnectionHandlerService) GetConnByPK(pk cipher.PubKey) (net.Conn, error) {
	//check if conn already added
	if conn, ok := ch.conns[pk]; ok {
		return conn, nil
	}
	return nil, fmt.Errorf("no conn available with the requested visor")
}

// AddConn adds the given net.Conn to the map to keep track of active connections
func (ch *ConnectionHandlerService) AddConn(pk cipher.PubKey, conn net.Conn) error {
	//check if conn already added
	if _, ok := ch.conns[pk]; ok {
		return fmt.Errorf("conn already added")
	}
	ch.conns[pk] = conn
	ch.log.Debugf("added conn %s	%s\n", conn.RemoteAddr().String(), conn.RemoteAddr().Network())
	return nil
}

// DeleteConn removes the given net.Conn from the map
func (ch *ConnectionHandlerService) DeleteConn(pk cipher.PubKey) error {
	//check if conn is added
	if _, ok := ch.conns[pk]; ok {
		delete(ch.conns, pk)
		return nil
	}
	return fmt.Errorf("pk has no connection") //? handle as error?
}

// ConnectionToPkHandled returns if a connection to the given pk is handled in a go routine
func (ch *ConnectionHandlerService) ConnectionToPkHandled(pk cipher.PubKey) bool {
	if _, ok := ch.handledConns[pk]; ok {
		return true
	}
	return false
}

// AddConnToHandled adds the given net.Conn to the map to keep track of handled connections
func (ch *ConnectionHandlerService) AddConnToHandled(pk cipher.PubKey, conn net.Conn) error {
	//check if conn already added
	if _, ok := ch.handledConns[pk]; ok {
		return fmt.Errorf("conn already added")
	}
	ch.conns[pk] = conn
	return nil
}

// DeleteConnFromHandled removes the given net.Conn from the handledConns map
func (ch *ConnectionHandlerService) DeleteConnFromHandled(pk cipher.PubKey) error {
	//check if conn is added
	if _, ok := ch.handledConns[pk]; ok {
		delete(ch.handledConns, pk)
		return nil
	}
	return fmt.Errorf("pk has no connection") //? handle as error?
}
