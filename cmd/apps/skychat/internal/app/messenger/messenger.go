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

	//only used as client/p2p
	SendRouteRequestMessage(route util.PKRoute) error
	SendLeaveChatMessage(pkroute util.PKRoute) error

	//used as client/p2p and server
	SendTextMessage(route util.PKRoute, msg []byte) error
	SendDeleteRoomMessage(route util.PKRoute) error
	SendAddRoomMessage(route util.PKRoute, info info.Info) error
	SendInfoMessage(pkroute util.PKRoute, root util.PKRoute, dest util.PKRoute, info info.Info) error
}
