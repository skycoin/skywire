package messenger

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
)

// Service interface is the interface to the service
type Service interface {
	Handle(pk cipher.PubKey) error
	Listen()
	SendTextMessage(pk cipher.PubKey, msg []byte) error
	SendChatRequestMessage(pk cipher.PubKey) error
	SendInfoMessage(pk cipher.PubKey, info info.Info) error
}
