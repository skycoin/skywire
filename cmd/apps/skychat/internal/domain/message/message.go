// Package message contains the code required by the chat app
package message

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// types of messages
const (
	// ErrMsgType is used to handle message errors types
	ErrMsgType = iota
	// ConnMsgType is used to handle message connections
	ConnMsgType
	// TxtMsgType is used to txt peers send to each other or within groups
	TxtMsgType
	// InfoMsgType is used to send and ask for info like what type of chat is the pk (group/peer), get all msgs, member infos etc.
	InfoMsgType
	// CmdMsgType is used to control a server (e.g. send ban-peer or delete-msg commands)
	CmdMsgType
)

// subtypes of ConnMsgType
const (
	// ErrConnMsg is used to handle connection error messages
	ErrConnMsg = iota
	// ConnMsgTypeRequest is used to handle connection message of type Request
	ConnMsgTypeRequest
	// ConnMsgTypeAccept is used to handle connection message of type Accept
	ConnMsgTypeAccept
	// ConnMsgTypeReject is used to handle connection message of type Reject
	ConnMsgTypeReject
	// ConnMsgTypeLeave is used to handle connection message of type Leave
	ConnMsgTypeLeave
	// ConnMsgTypeDelete is used to handle connection message of type Delete
	ConnMsgTypeDelete
)

// subtypes of InfoMsgType
const (
	//ErrInfoMsgType is used to handle info error messages
	ErrInfoMsgType = iota
	//InfoMsgTypeSingle is used to handle the info of a single user or server or room
	InfoMsgTypeSingle
	//InfoMsgTypeServerMembers is used to update the list of members of a server
	InfoMsgTypeServerMembers
	//InfoMsgTypeRoomMembers is used to update the list of members of a room
	InfoMsgTypeRoomMembers
	//FUTUREFEATURES: Admins, Moderators, Muted, Blacklist, Whitelist, Rooms from Server that are visible
)

// types of messageStatus
const (
	// MsgStatusInitial is used handle message status Initial
	MsgStatusInitial = iota
	// ConnMsgTypeRequest is used handle message status Sent
	MsgStatusSent
	// ConnMsgTypeRequest is used handle message status Received
	MsgStatusReceived
)

// types fo CmdMsgType
const (
	// ErrCmdMsg is used to handle command error messages
	ErrCmdMsg = iota
	// CmdMsgTypeAddRoom is used to add a room
	CmdMsgTypeAddRoom
	// CmdMsgTypeDeleteRoom is used to delete a room
	CmdMsgTypeDeleteRoom
	/*CmdMsgTypeMutePeer
	CmdMsgTypeUnmutePeer
	CmdMsgTypeBanPeer
	CmdMsgTypeUnbanPeer
	CmdMsgTypeHireAdmin
	CmdMsgTypeFireAdmin
	CmdMsgTypeHireModerator
	CmdMsgTypeFireModerator*/
)

// Message defines a message
type Message struct {
	ID         int           `json:"Id"`         //an identifier for p2p chats and groups, Id is set by the receiver/server
	Origin     cipher.PubKey `json:"Origin"`     //the originator of the Message
	Time       time.Time     `json:"Time"`       //the utc+0 timestamp of the Message
	Root       util.PKRoute  `json:"Root"`       //the root from where the Message was received (e.g. peer/group)
	Dest       util.PKRoute  `json:"Dest"`       //the destination where the message should be sent.
	MsgType    int           `json:"Msgtype"`    //see const above
	MsgSubtype int           `json:"MsgSubtype"` //see const above
	Message    []byte        `json:"Message"`    //the actual Message
	Status     int           `json:"Status"`     //"Sent" or "Received"
	Seen       bool          `json:"Seen"`       //flag to save whether the Message was read or not by the receiver (only for local notifications) -> online feedback will be implemented in future versions
}

// RAWMessage defines a raw json message
type RAWMessage struct {
	ID         int             `json:"Id"`
	Origin     cipher.PubKey   `json:"Origin"`
	Time       time.Time       `json:"Time"`
	Root       util.PKRoute    `json:"Root"`
	Dest       util.PKRoute    `json:"Dest"`
	MsgType    int             `json:"Msgtype"`
	MsgSubtype int             `json:"MsgSubtype"`
	Message    json.RawMessage `json:"Message"`
	Status     int             `json:"Status"`
	Seen       bool            `json:"Seen"`
}

// JSONMessage defines a json message
type JSONMessage struct {
	ID         int           `json:"Id"`
	Origin     cipher.PubKey `json:"Origin"`
	Time       time.Time     `json:"Time"`
	Root       util.PKRoute  `json:"Root"`
	Dest       util.PKRoute  `json:"Dest"`
	MsgType    int           `json:"Msgtype"`
	MsgSubtype int           `json:"MsgSubtype"`
	Message    string        `json:"Message"`
	Status     int           `json:"Status"`
	Seen       bool          `json:"Seen"`
}

// NewJSONMessage return a JSONMessage from a message
func NewJSONMessage(m Message) JSONMessage {
	return JSONMessage{
		m.ID,
		m.Origin,
		m.Time,
		m.Root,
		m.Dest,
		m.MsgType,
		m.MsgSubtype,
		string(m.Message),
		m.Status,
		m.Seen,
	}
}

// NewRAWMessage return a RAWMessage from a message
func NewRAWMessage(m Message) RAWMessage {

	bytes, err := json.Marshal(m.Message)
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	return RAWMessage{
		m.ID,
		m.Origin,
		m.Time,
		m.Root,
		m.Dest,
		m.MsgType,
		m.MsgSubtype,
		bytes,
		m.Status,
		m.Seen,
	}
}

// NewMessage returns a message from a JSONMessage
func NewMessage(m RAWMessage) Message {

	var data []byte
	Source := (*json.RawMessage)(&m.Message)
	err := json.Unmarshal(*Source, &data)
	if err != nil {
		fmt.Printf("%v", err)
	}

	return Message{
		m.ID,
		m.Origin,
		m.Time,
		m.Root,
		m.Dest,
		m.MsgType,
		m.MsgSubtype,
		data,
		m.Status,
		m.Seen,
	}
}

// MarshalJSON returns marshaled json message and error
func (m Message) MarshalJSON() ([]byte, error) {
	return json.Marshal(NewJSONMessage(m))
}

// NewTextMessage returns a Message
func NewTextMessage(pkOrigin cipher.PubKey, routeDestination util.PKRoute, msg []byte) Message {
	m := Message{}
	m.Origin = pkOrigin
	m.Root = util.NewP2PRoute(pkOrigin)
	m.Dest = routeDestination
	m.MsgType = TxtMsgType
	m.MsgSubtype = 0
	m.Message = msg
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewRouteRequestMessage returns a request Message
func NewRouteRequestMessage(pkOrigin cipher.PubKey, routeDestination util.PKRoute) Message {
	m := Message{}
	m.Origin = pkOrigin
	m.Root = util.NewP2PRoute(pkOrigin)
	m.Dest = routeDestination
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeRequest
	m.Message = []byte("Chat Request")
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

/* NewChatAcceptMessage returns a chat accepted message
pk is the users pk to set the messages root
*/
func NewChatAcceptMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeAccept
	m.Status = MsgStatusInitial
	m.Message = []byte("Chat Accepted")
	m.Time = time.Now()
	return m
}

// NewChatRejectMessage returns new chat rejected message
func NewChatRejectMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeReject
	m.Message = []byte("Chat Rejected")
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewChatLeaveMessage returns new chat leave message
func NewChatLeaveMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeLeave
	m.Message = []byte("Chat Left")
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewRouteDeletedMessage returns new message to info about deleted route
func NewRouteDeletedMessage(route util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = route.Visor
	m.Root = route
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeDelete
	m.Message = []byte("Chat Deleted")
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewChatInfoMessage returns new chat info
func NewChatInfoMessage(root util.PKRoute, dest util.PKRoute, info []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = InfoMsgType
	m.MsgSubtype = InfoMsgTypeSingle
	m.Message = info
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewAddRoomMessage returns a Message
func NewAddRoomMessage(root util.PKRoute, dest util.PKRoute, info []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeAddRoom
	m.Message = info
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewDeleteRoomMessage returns a Message
func NewDeleteRoomMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeDeleteRoom
	m.Message = []byte("Room Deleted")
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// NewRoomMembersMessage returns a Message of roomMembers
func NewRoomMembersMessage(root util.PKRoute, dest util.PKRoute, members []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = InfoMsgType
	m.MsgSubtype = InfoMsgTypeRoomMembers
	m.Message = members
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

// GetID returns message ID
func (m *Message) GetID() int {
	return m.ID
}

// GetOrigin returns origin public key
func (m *Message) GetOrigin() cipher.PubKey {
	return m.Origin
}

// GetTime returns time.Time of the message
func (m *Message) GetTime() time.Time {
	return m.Time
}

// GetRootVisor returns the root visor public key
func (m *Message) GetRootVisor() cipher.PubKey {
	return m.Root.Visor
}

// GetRootServer returns the root server public key
func (m *Message) GetRootServer() cipher.PubKey {
	return m.Root.Server
}

// GetRootRoom returns the root room public key
func (m *Message) GetRootRoom() cipher.PubKey {
	return m.Root.Room
}

// GetDestinationVisor returns the destination visor
func (m *Message) GetDestinationVisor() cipher.PubKey {
	return m.Dest.Visor
}

// GetDestinationServer returns the destination server
func (m *Message) GetDestinationServer() cipher.PubKey {
	return m.Dest.Server
}

// GetDestinationRoom returns the destination server
func (m *Message) GetDestinationRoom() cipher.PubKey {
	return m.Dest.Room
}

// GetMessageType returns the message type integer
func (m *Message) GetMessageType() int {
	return m.MsgType
}

// GetMessage returns the message in bytes
func (m *Message) GetMessage() []byte {
	return m.Message
}

// GetStatus returns the message status int
func (m *Message) GetStatus() int {
	return m.Status
}

// GetSeen returns the read status of the message
func (m *Message) GetSeen() bool {
	return m.Seen
}

// SetStatus sets the message status
func (m *Message) SetStatus(status int) {
	m.Status = status
}
