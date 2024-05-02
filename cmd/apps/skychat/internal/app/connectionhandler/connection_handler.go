// Package connectionhandler contains the interface Service required by the chat app
package connectionhandler

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Service interface is the interface to the service
type Service interface {
	//HandleConnection(pk cipher.PubKey)
	UnhandleConnection(pk cipher.PubKey) error
	Listen()

	GetReceiveChannel() chan message.Message
	SendMessage(pkroute util.PKRoute, m message.Message, addToDatabase bool) error
}
