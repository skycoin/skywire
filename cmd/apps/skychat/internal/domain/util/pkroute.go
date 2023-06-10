// Package util collects all structs and functions needed inside skychat
package util

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// PKRoute defines the routing inside the skychat app if a root or destination is from a p2p chat or a specific room of a specific server.
type PKRoute struct {
	Visor  cipher.PubKey // PK of visor
	Server cipher.PubKey // P2P: Server=Visor // Server: PK of server
	Room   cipher.PubKey // P2P: Room=nil     // Server: PK of room
}

// String returns a string representation of PKRoute
func (r *PKRoute) String() string {
	return "pkVisor: " + r.Visor.Hex() + " pkServer: " + r.Server.Hex() + " pkRoom: " + r.Room.Hex()
}
