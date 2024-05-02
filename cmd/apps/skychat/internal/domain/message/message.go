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
	// StatusMsgType is used to let peers know if their message was received/rejected ...
	StatusMsgType
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
	//InfoMsgTypeRoomMuted is used to update the list of muted of a room
	InfoMsgTypeRoomMuted
	//FUTUREFEATURES: Admins, Moderators, Muted, Blacklist, Whitelist, Rooms from Server that are visible
)

// types of messageStatus
const (
	// MsgStatusInitial is used handle message status Initial
	MsgStatusInitial = iota
	// MsgStatusSent is used handle message status Sent
	MsgStatusSent
	// MsgStatusReceived is used handle message status Received
	MsgStatusReceived
	// MsgStatusRejected is used to handle message status Rejected
	MsgStatusRejected
)

// types fo CmdMsgType
const (
	// ErrCmdMsg is used to handle command error messages
	ErrCmdMsg = iota
	// CmdMsgTypeAddRoom is used to add a room
	CmdMsgTypeAddRoom
	// CmdMsgTypeDeleteRoom is used to delete a room
	CmdMsgTypeDeleteRoom
	// CmdMsgTypeMutePeer is used to mute a peer
	CmdMsgTypeMutePeer
	// CmdMsgTypeUnmutePeer is used tu unmute a peer
	CmdMsgTypeUnmutePeer
	// CmdMsgTypeHireModerator is used to hire a peer as moderator
	CmdMsgTypeHireModerator
	// CmdMsgTypeFireModerator is used to fire a moderator
	CmdMsgTypeFireModerator
	/*CmdMsgTypeBanPeer
	CmdMsgTypeUnbanPeer
	CmdMsgTypeHireAdmin
	CmdMsgTypeFireAdmin
	*/
)

// Message defines a message
type Message struct {
	ID         string        `json:"Id"`         //an identifier for p2p chats and groups
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
	ID         string          `json:"Id"`
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
	ID         string        `json:"Id"`
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

// NewMessageFromJSON returns a Message from a JSONMessage
func NewMessageFromJSON(m JSONMessage) Message {
	return Message{
		m.ID,
		m.Origin,
		m.Time,
		m.Root,
		m.Dest,
		m.MsgType,
		m.MsgSubtype,
		[]byte(m.Message),
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

// UnmarshalJSON returns unmarshaled bytes and error
func (m *Message) UnmarshalJSON(b []byte) error {
	jm := JSONMessage{}
	err := json.Unmarshal(b, &jm)
	if err != nil {
		return err
	}

	m.ID = jm.ID
	m.Origin = jm.Origin
	m.Time = jm.Time
	m.Root = jm.Root
	m.Dest = jm.Dest
	m.MsgType = jm.MsgType
	m.MsgSubtype = jm.MsgSubtype
	m.Message = []byte(jm.Message)
	m.Status = jm.Status
	m.Seen = jm.Seen

	return nil
}

// GetID returns message ID
func (m *Message) GetID() string {
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

// IsFromRemoteP2PToLocalP2P returns if the message is a p2p message from remote to local
func (m *Message) IsFromRemoteP2PToLocalP2P(localPK cipher.PubKey) bool {
	return m.GetDestinationVisor() == localPK && m.GetDestinationServer() == localPK && m.GetDestinationRoom() == localPK && m.GetRootVisor() == m.GetRootServer() && m.GetRootServer() == m.GetRootRoom()
}

// IsFromLocalToRemoteP2P returns if the message is a p2p message from local to remote
func (m *Message) IsFromLocalToRemoteP2P() bool {
	return m.GetDestinationVisor() == m.GetDestinationServer()
}

// IsFromRemoteServer returns if the message is a message of a remote server
func (m *Message) IsFromRemoteServer() bool {
	return m.GetRootVisor() != m.GetRootServer() && m.GetRootServer() != m.GetRootRoom()
}

// IsFromRemoteToLocalServer returns if the message is a message sent to the local server
func (m *Message) IsFromRemoteToLocalServer(localPK cipher.PubKey) bool {
	return m.GetDestinationVisor() == localPK && m.GetDestinationVisor() != m.GetDestinationServer() && m.GetDestinationServer() != m.GetDestinationRoom()
}

// PrettyPrint uses fmt.Prints for beautiful representation of message
func (m *Message) PrettyPrint(printMessageBytes bool) string {
	prettyPrint := ""
	prettyPrint = fmt.Sprintf(prettyPrint + "Message:	---------------------------------------------------------------------------------------------------\n")
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	ID: 		%s \n", m.ID)
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Origin:		%s \n", m.Origin)
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Time:		%s \n", m.Time)
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Root:		pkVisor:  %s \n", m.Root.Visor.Hex())
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:			pkServer: %s \n", m.Root.Server.Hex())
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:			pkRoom:   %s \n", m.Root.Room.Hex())
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Dest:		pkVisor:  %s \n", m.Dest.Visor.Hex())
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:			pkServer: %s \n", m.Dest.Server.Hex())
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:			pkRoom:   %s \n", m.Dest.Room.Hex())
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	MsgType:	%d \n", m.MsgType)
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	MsgSubType:	%d \n", m.MsgSubtype)

	if printMessageBytes {
		prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Message:		%s \n", string(m.Message))
	}

	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Status:		%d \n", m.Status)
	prettyPrint = fmt.Sprintf(prettyPrint+"Message:	Seen:		%t \n", m.Seen)
	prettyPrint = fmt.Sprintf(prettyPrint + "Message:	---------------------------------------------------------------------------------------------------\n")

	return prettyPrint
}

// PrettyPrintTextMessage uses fmt.Prints for beautiful representation of a TextMessage
func (m *Message) PrettyPrintTextMessage() string {
	prettyPrint := ""
	prettyPrint = fmt.Sprintf(prettyPrint + "TextMessage: ---------------------------------------------------------------------------------------------------")
	prettyPrint = fmt.Sprintf(prettyPrint+"TextMessage:	Text:	%s \n", m.Message)
	prettyPrint = fmt.Sprintf(prettyPrint+"TextMessage:	Status: %d \n", m.Status)
	prettyPrint = fmt.Sprintf(prettyPrint + "TextMessage: ---------------------------------------------------------------------------------------------------")

	return prettyPrint
}
