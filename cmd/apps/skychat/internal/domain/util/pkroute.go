// Package util collects all structs and functions needed inside skychat
package util

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// PKRoute defines the routing inside the skychat app if a root or destination is from a p2p chat or a specific room of a specific server.
// P2PRoute: 	VisorPK == ServerPK == RoomPK
// ServerRoute: VisorPK != ServerPK && ServerPK == RoomPK
// RoomRoute: 	VisorPK != ServerPK && ServerPK != RoomPK
type PKRoute struct {
	Visor  cipher.PubKey
	Server cipher.PubKey
	Room   cipher.PubKey
}

// String returns a string representation of PKRoute
func (r *PKRoute) String() string {
	return "pkVisor: " + r.Visor.Hex() + " pkServer: " + r.Server.Hex() + " pkRoom: " + r.Room.Hex()
}

// IsP2PRoute returns if the route is a p2p route
func (r *PKRoute) IsP2PRoute() bool {
	return r.Visor == r.Server && r.Server == r.Room
}

// IsServerRoute returns if the route is a server route
func (r *PKRoute) IsServerRoute() bool {
	return r.Visor != r.Server && r.Server == r.Room
}

// IsRoomRoute returns if the route is a room route
func (r *PKRoute) IsRoomRoute() bool {
	return r.Visor != r.Server && r.Server != r.Room
}
