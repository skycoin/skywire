// Package util collects all structs and functions needed inside skychat
package util

import "github.com/skycoin/skywire-utilities/pkg/cipher"

// NewVisorOnlyRoute returns a route with only the visor pubkey
func NewVisorOnlyRoute(pk cipher.PubKey) PKRoute {
	pkr := PKRoute{}
	pkr.Visor = pk
	return pkr
}

// NewP2PRoute returns a route with visor pubkey == server pubkey
func NewP2PRoute(visorpk cipher.PubKey) PKRoute {
	pkr := PKRoute{}
	pkr.Visor = visorpk
	pkr.Server = visorpk
	pkr.Room = visorpk
	return pkr
}

// NewServerRoute returns a new route of a server
// This is achieved by setting Room == Server
func NewServerRoute(visorpk cipher.PubKey, serverpk cipher.PubKey) PKRoute {
	pkr := PKRoute{}
	pkr.Visor = visorpk
	pkr.Server = serverpk
	pkr.Room = serverpk
	return pkr
}

// NewRoomRoute returns a new route of a room
func NewRoomRoute(visorpk cipher.PubKey, serverpk cipher.PubKey, roompk cipher.PubKey) PKRoute {
	pkr := PKRoute{}
	pkr.Visor = visorpk
	pkr.Server = serverpk
	pkr.Room = roompk
	return pkr
}

// NewLocalServerRoute sets up a new local defined server route
func NewLocalServerRoute(visorPK cipher.PubKey, existingServer map[cipher.PubKey]bool) PKRoute {

	serverPK := cipher.PubKey{}

	for ok := true; ok; ok = !existingServer[serverPK] {
		serverPK, _ = cipher.GenerateKeyPair()
		existingServer[serverPK] = true

	}

	r := NewServerRoute(visorPK, serverPK)
	return r
}

// NewLocalRoomRoute sets up a new local  defined room route
func NewLocalRoomRoute(visorPK cipher.PubKey, serverPK cipher.PubKey, existingRooms map[cipher.PubKey]bool) PKRoute {
	roomPK := cipher.PubKey{}

	for ok := true; ok; ok = !existingRooms[roomPK] {
		roomPK, _ = cipher.GenerateKeyPair()
		existingRooms[roomPK] = true
	}

	r := NewRoomRoute(visorPK, serverPK, roomPK)
	return r
}
