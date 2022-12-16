// Package messenger contains the interface Service required by the chat app
package messenger

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Service interface is the interface to the service
type Service interface {
	Handle(pk cipher.PubKey)
	Listen()
	SendTextMessage(route util.PKRoute, msg []byte) error
	SendRouteRequestMessage(route util.PKRoute) error
	SendInfoMessage(root util.PKRoute, dest util.PKRoute, info info.Info) error
}
