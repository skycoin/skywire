package chat

import (
	"net"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
)

const (
	errChatType = iota
	GroupChat
	PeerChat
)

type Chat struct {
	PK    cipher.PubKey
	CType int
	Conn  net.Conn
	Info  info.Info
	Msgs  []message.Message

	//GroupChat
	peers []peer.Peer
}

// Getter
func (c *Chat) GetPK() cipher.PubKey {
	return c.PK
}

func (c *Chat) GetType() int {
	return c.CType
}

func (c *Chat) GetConnection() net.Conn {
	return c.Conn
}

func (c *Chat) GetInfo() info.Info {
	return c.Info
}

func (c *Chat) GetMessages() []message.Message {
	return c.Msgs
}

func (c *Chat) GetPeers() []peer.Peer {
	return c.peers
}

//Setter

func (c *Chat) SetConnection(Conn net.Conn) {
	c.Conn = Conn
}

func (c *Chat) DeleteConnection() {
	c.Conn = nil
}

//Add the given message to the given chat
func (c *Chat) AddMessage(m message.Message) {
	c.Msgs = append(c.Msgs, m)
}

//Constructors

func NewUndefinedChat(PK cipher.PubKey) Chat {
	c := Chat{}
	c.PK = PK
	c.CType = errChatType
	c.Conn = nil
	c.Info = info.NewDefaultInfo()
	c.Msgs = []message.Message{}
	return c
}

func NewChat(PK cipher.PubKey, CType int, i info.Info, Msgs []message.Message) Chat {
	c := Chat{}
	c.PK = PK
	c.CType = CType
	c.Info = i
	c.Msgs = Msgs
	return c
}
