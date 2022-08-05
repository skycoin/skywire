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
	// GroupChat is a group chat
	GroupChat
	// PeerChat is a direct message
	PeerChat
)

// Chat is the struct of the chat itself
type Chat struct {
	PK    cipher.PubKey
	CType int
	Conn  net.Conn
	Info  info.Info
	Msgs  []message.Message

	//GroupChat
	peers []peer.Peer
}

// GetPK gets the public key
func (c *Chat) GetPK() cipher.PubKey {
	return c.PK
}

// GetType gets the chat type
func (c *Chat) GetType() int {
	return c.CType
}

// GetConnection returns net.Conn
func (c *Chat) GetConnection() net.Conn {
	return c.Conn
}

// GetInfo returns info.Info
func (c *Chat) GetInfo() info.Info {
	return c.Info
}

// GetMessages returns []message.Message
func (c *Chat) GetMessages() []message.Message {
	return c.Msgs
}

// GetPeers returns []peer.Peer
func (c *Chat) GetPeers() []peer.Peer {
	return c.peers
}

//Setter

// SetConnection sets the connection type used
func (c *Chat) SetConnection(Conn net.Conn) {
	c.Conn = Conn
}

// DeleteConnection clears the connection
func (c *Chat) DeleteConnection() {
	c.Conn = nil
}

// AddMessage Add the given message to the given chat
func (c *Chat) AddMessage(m message.Message) {
	c.Msgs = append(c.Msgs, m)
}

// Constructors

// NewUndefinedChat creates undefined empty chat to a public key
func NewUndefinedChat(PK cipher.PubKey) Chat {
	c := Chat{}
	c.PK = PK
	c.CType = errChatType
	c.Conn = nil
	c.Info = info.NewDefaultInfo()
	c.Msgs = []message.Message{}
	return c
}

// NewChat creates a new chat
func NewChat(PK cipher.PubKey, CType int, i info.Info, Msgs []message.Message) Chat {
	c := Chat{}
	c.PK = PK
	c.CType = CType
	c.Info = i
	c.Msgs = Msgs
	return c
}
