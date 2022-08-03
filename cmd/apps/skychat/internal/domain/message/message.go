package message

import (
	"encoding/json"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

//types of messages
const (
	ErrMsgType  = iota
	ConnMsgType //used to handle connections
	TxtMsgType  //the txt peers send to each other or within groups
	InfoMsgType //used to send and ask for info like what type of chat is the pk (group/peer), get all msgs, member infos etc.
	CmdMsgType  //used to control a server (e.g. send ban-peer or delete-msg commands)
)

//subtypes of connMsgType
const (
	ErrConnMsg = iota
	ConnMsgTypeRequest
	ConnMsgTypeAccept
	ConnMsgTypeReject
)

//types of messageStatus
const (
	MsgStatusInitial = iota
	MsgStatusSent
	MsgStatusReceived
)

type Message struct {
	ID         int64         //an identifier for p2p chats and groups, Id is set by the receiver/server
	Origin     cipher.PubKey //the originator of the Message
	Time       time.Time     //the utc+0 timestamp of the Message
	Sender     cipher.PubKey //from who the Message was received (e.g. peer/group)
	Msgtype    int           //see const above
	MsgSubtype int           //see consts above
	Message    []byte        //the actual Message
	Status     int           //"Sent" or "Received"
	Seen       bool          //flag to save whether the Message was read or not by the receiver (only for local notifications) -> online feedback will be implemented in future versions
}

type JSONMessage struct {
	ID         int64         `json:"Id"`
	Origin     cipher.PubKey `json:"Origin"`
	Time       time.Time     `json:"Time"`
	Sender     cipher.PubKey `json:"Sender"`
	Msgtype    int           `json:"Msgtype"`
	MsgSubtype int           `json:"MsgSubtype"`
	Message    string        `json:"Message"`
	Status     int           `json:"Status"`
	Seen       bool          `json:"Seen"`
}

func NewJSONMessage(m Message) JSONMessage {
	return JSONMessage{
		m.ID,
		m.Origin,
		m.Time,
		m.Sender,
		m.Msgtype,
		m.MsgSubtype,
		string(m.Message),
		m.Status,
		m.Seen,
	}
}

func NewMessage(m JSONMessage) Message {
	return Message{
		m.ID,
		m.Origin,
		m.Time,
		m.Sender,
		m.Msgtype,
		m.MsgSubtype,
		[]byte(m.Message),
		m.Status,
		m.Seen,
	}
}

func (m Message) MarshalJSON() ([]byte, error) {
	return json.Marshal(NewJSONMessage(m))
}

func NewTextMessage(pk cipher.PubKey, msg []byte) Message {
	m := Message{}
	m.Origin = pk
	m.Sender = pk
	m.Msgtype = TxtMsgType
	m.MsgSubtype = 0
	m.Message = msg
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

func NewChatRequestMessage(pk cipher.PubKey) Message {
	m := Message{}
	m.Origin = pk
	m.Sender = pk
	m.Msgtype = ConnMsgType
	m.MsgSubtype = ConnMsgTypeRequest
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

func NewChatAcceptMessage(pk cipher.PubKey) Message {
	m := Message{}
	m.Origin = pk
	m.Sender = pk
	m.Msgtype = ConnMsgType
	m.MsgSubtype = ConnMsgTypeAccept
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

func NewChatRejectMessage(pk cipher.PubKey) Message {
	m := Message{}
	m.Origin = pk
	m.Sender = pk
	m.Msgtype = ConnMsgType
	m.MsgSubtype = ConnMsgTypeReject
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

func NewChatInfoMessage(pk cipher.PubKey, info []byte) Message {
	m := Message{}
	m.Origin = pk
	m.Sender = pk
	m.Msgtype = InfoMsgType
	m.Message = info
	m.Status = MsgStatusInitial
	m.Time = time.Now()
	return m
}

func (m *Message) GetId() int64 {
	return m.ID
}

func (m *Message) GetOrigin() cipher.PubKey {
	return m.Origin
}

func (m *Message) GetTime() time.Time {
	return m.Time
}

func (m *Message) GetSender() cipher.PubKey {
	return m.Sender
}

func (m *Message) GetMessageType() int {
	return m.Msgtype
}

func (m *Message) GetMessage() []byte {
	return m.Message
}

func (m *Message) GetStatus() int {
	return m.Status
}

func (m *Message) GetSeen() bool {
	return m.Seen
}

func (m *Message) SetStatus(status int) {
	m.Status = status
}
