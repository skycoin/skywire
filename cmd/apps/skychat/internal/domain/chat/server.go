package chat

import (
	"fmt"
	"net"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Server defines a server for a collection of rooms
type Server struct {
	//Public
	PKRoute util.PKRoute
	Info    info.Info // the public info of the server

	Members   map[cipher.PubKey]peer.Peer // all members
	Admins    map[cipher.PubKey]bool      // all admins (can do everything that mods can do but on all rooms and can hire and unhire mods, can add pks to blacklist)
	Muted     map[cipher.PubKey]bool      // all members muted for all rooms
	Blacklist map[cipher.PubKey]bool      // Blacklist to block inocming connections
	Whitelist map[cipher.PubKey]bool      // maybe also a whitelist, so only specific members can connect
	Rooms     map[cipher.PubKey]Room      // all rooms of the server

	//? Maybe also add a Messages []message.Message here for "logging purposes" e.g. "Requested to join server", "Join Request accepted", "Request to join Room" etc.

	//only for local server
	Conns map[cipher.PubKey]net.Conn // active peer connections
}

// AddMessage adds a message to the server
func (s *Server) AddMessage(pkroute util.PKRoute, m message.Message) {
	r := s.Rooms[pkroute.Room]
	r.AddMessage(m)
	s.Rooms[pkroute.Room] = r
}

// SetRouteInfo sets the info of the given room inside the server
func (s *Server) SetRouteInfo(pkroute util.PKRoute, info info.Info) error {
	if pkroute.Server == pkroute.Room {
		s.SetInfo(info)
	}

	room, err := s.GetRoomByPK(pkroute.Room)
	if err != nil {
		return err
	}
	room.SetInfo(info)

	err = s.SetRoom(*room)
	if err != nil {
		return err
	}

	return nil
}

// GetPKRoute returns the PKRoute
func (s *Server) GetPKRoute() util.PKRoute {
	return s.PKRoute
}

// SetInfo sets the server's rinfo to the given info
func (s *Server) SetInfo(info info.Info) {
	s.Info = info
}

// GetInfo returns the info of the server
func (s *Server) GetInfo() info.Info {
	return s.Info
}

// AddRoom adds the given room to the server
func (s *Server) AddRoom(room Room) error {
	_, err := s.GetRoomByPK(room.PKRoute.Room)
	if err != nil {
		s.Rooms[room.PKRoute.Room] = room
		return nil

	}
	return fmt.Errorf("room already exists in server")
}

// DeleteRoom removes the given room from the server
func (s *Server) DeleteRoom(pk cipher.PubKey) error {
	_, err := s.GetRoomByPK(pk)
	if err != nil {
		return fmt.Errorf("room does not exist") //? handle as error?

	}
	delete(s.Rooms, pk)
	return nil

}

// GetRoomByPK returns the the room mapped by pk if available and returns err if no room with given pk is available
func (s *Server) GetRoomByPK(pk cipher.PubKey) (*Room, error) {
	if room, ok := s.Rooms[pk]; ok {
		return &room, nil
	}
	return nil, fmt.Errorf("no room with pk %s found in visor %s and server %s", pk.Hex(), s.PKRoute.Visor, s.PKRoute.Server)
}

// SetRoom updates the given room
func (s *Server) SetRoom(room Room) error {
	//check if room exists
	if _, ok := s.Rooms[room.PKRoute.Room]; ok {
		s.Rooms[room.PKRoute.Room] = room
		return nil
	}
	return fmt.Errorf("no room with pk %s found in server %s", room.PKRoute.Room.Hex(), s.PKRoute.Server)
}

// GetAllRooms returns a room-map of all rooms
func (s *Server) GetAllRooms() map[cipher.PubKey]Room {
	return s.Rooms
}

// GetAllRoomsBoolMap returns a bool-map of all rooms
func (s *Server) GetAllRoomsBoolMap() map[cipher.PubKey]bool {
	r := make(map[cipher.PubKey]bool)
	for k := range s.Rooms {
		r[k] = true
	}
	return r
}

// AddMember adds the given peer to the server
func (s *Server) AddMember(peer peer.Peer) error {
	_, err := s.GetMemberByPK(peer.GetPK())
	if err != nil {
		s.Members[peer.GetPK()] = peer
		return nil

	}
	return fmt.Errorf("peer already member of server")

}

// DeleteMember deletes the member with the given pk
func (s *Server) DeleteMember(pk cipher.PubKey) error {
	_, err := s.GetMemberByPK(pk)
	if err != nil {
		delete(s.Members, pk)
		return nil
	}
	//TODO: also try to delete member from all rooms as he leaves the server
	delete(s.Members, pk)
	return nil
}

// GetMemberByPK returns the the member mapped by pk if available and returns err if no member with given pk exists
func (s *Server) GetMemberByPK(pk cipher.PubKey) (*peer.Peer, error) {
	if member, ok := s.Members[pk]; ok {
		return &member, nil
	}
	return nil, fmt.Errorf("member does not exist")
}

// SetMember updates the given peer
func (s *Server) SetMember(peer peer.Peer) error {
	//check if peer exists
	if _, ok := s.Members[peer.GetPK()]; ok {
		s.Members[peer.GetPK()] = peer
		return nil
	}
	return fmt.Errorf("member does not exist")
}

// GetAllMembers returns all members (peers) of the server
func (s *Server) GetAllMembers() map[cipher.PubKey]peer.Peer {
	return s.Members
}

// SetMemberInfo sets the given info of the member inside the server and all rooms
func (s *Server) SetMemberInfo(i info.Info) error {
	//set member info in server
	sMember, err := s.GetMemberByPK(i.GetPK())
	if err != nil {
		return err
	}
	sMember.SetInfo(i)
	err = s.SetMember(*sMember)
	if err != nil {
		return err
	}

	//set info in rooms if the pk is member
	for _, room := range s.Rooms {
		rMember, err := room.GetMemberByPK(i.GetPK())
		if err != nil {
			continue
		}
		rMember.SetInfo(i)
		err = room.SetMember(*rMember)
		if err != nil {
			return err
		}
		err = s.SetRoom(room)
		if err != nil {
			return err
		}
	}

	return nil
}

// AddAdmin adds the given pk (peer) as admin of the server
func (s *Server) AddAdmin(pk cipher.PubKey) error {
	//check if peer is already admin
	if _, ok := s.Admins[pk]; ok {
		return fmt.Errorf("peer is already admin")
	}
	s.Admins[pk] = true
	return nil
}

// DeleteAdmin removes the given pk (peer) as admin from the server
func (s *Server) DeleteAdmin(pk cipher.PubKey) error {
	//check if peer is admin
	if _, ok := s.Admins[pk]; ok {
		delete(s.Admins, pk)
		return nil
	}
	return fmt.Errorf("member is no admin") //? handle as error?
}

// GetAllAdmin returns all admins
func (s *Server) GetAllAdmin() map[cipher.PubKey]bool {
	return s.Admins
}

// AddMuted mutes the given pk (peer)
func (s *Server) AddMuted(pk cipher.PubKey) error {
	//check if peer already muted
	if _, ok := s.Muted[pk]; ok {
		return fmt.Errorf("peer already muted")
	}
	s.Muted[pk] = true //?maybe one day make map of type "Time-stamp" or something similar to enable timed mutes
	return nil
}

// DeleteMuted removes the given pk (peer) from muted status
func (s *Server) DeleteMuted(pk cipher.PubKey) error {
	//check if peer is muted
	if _, ok := s.Muted[pk]; ok {
		delete(s.Muted, pk)
		return nil
	}
	return fmt.Errorf("member is not muted") //? handle as error?
}

// GetAllMuted returns all muted members/peers
func (s *Server) GetAllMuted() map[cipher.PubKey]bool {
	return s.Muted
}

// AddToBlacklist blocks the given pk from joining the server
func (s *Server) AddToBlacklist(pk cipher.PubKey) error {
	//check if peer already blacklisted
	if _, ok := s.Blacklist[pk]; ok {
		return fmt.Errorf("peer already blacklisted")
	}
	s.Blacklist[pk] = true
	return nil
}

// DeleteFromBlacklist unblocks the given pk from joining the server
func (s *Server) DeleteFromBlacklist(pk cipher.PubKey) error {
	//check if peer is blacklisted
	if _, ok := s.Blacklist[pk]; ok {
		delete(s.Blacklist, pk)
		return nil
	}
	return fmt.Errorf("member is not blacklisted") //? handle as error?
}

// GetBlacklist returns all blacklisted/banned members/peers
func (s *Server) GetBlacklist() map[cipher.PubKey]bool {
	return s.Blacklist
}

// AddToWhitelist adds a pk to the join-only-list of the server
func (s *Server) AddToWhitelist(pk cipher.PubKey) error {
	//check if peer already whitelisted
	if _, ok := s.Whitelist[pk]; ok {
		return fmt.Errorf("peer already whitelisted")
	}
	s.Whitelist[pk] = true
	return nil
}

// DeleteFromWhitelist removes a pk from the join-only-list of the server
func (s *Server) DeleteFromWhitelist(pk cipher.PubKey) error {
	//check if peer is whitelisted
	if _, ok := s.Whitelist[pk]; ok {
		delete(s.Whitelist, pk)
		return nil
	}
	return fmt.Errorf("member is not whitelisted") //? handle as error?
}

// GetWhitelist returns all whitelisted members/peers
func (s *Server) GetWhitelist(pk cipher.PubKey) map[cipher.PubKey]bool {
	return s.Whitelist
}

// AddConn adds the given net.Conn to the server to keep track of connected peers
func (s *Server) AddConn(pk cipher.PubKey, conn net.Conn) error {
	//check if conn already added
	if _, ok := s.Conns[pk]; ok {
		return fmt.Errorf("conn already added")
	}
	s.Conns[pk] = conn
	return nil
}

// DeleteConn removes the given net.Conn from the server
func (s *Server) DeleteConn(pk cipher.PubKey) error {
	//check if conn is added
	if _, ok := s.Conns[pk]; ok {
		delete(s.Conns, pk)
		return nil
	}
	return fmt.Errorf("pk has no connection") //? handle as error?
}

// GetAllConns returns all connections
func (s *Server) GetAllConns() map[cipher.PubKey]net.Conn {
	return s.Conns
}

// GetConnByPK returns connection of PK
func (s *Server) GetConnByPK(pk cipher.PubKey) (*net.Conn, error) {
	if conn, ok := s.Conns[pk]; ok {
		return &conn, nil
	}
	return nil, fmt.Errorf("connection of pk does not exist")
}

// NewLocalServer returns a new local server
func NewLocalServer(serverRoute util.PKRoute, i info.Info) (*Server, error) {
	s := Server{}
	s.PKRoute = serverRoute
	s.Info = i

	s.Members = make(map[cipher.PubKey]peer.Peer)
	s.Admins = make(map[cipher.PubKey]bool)
	s.Muted = make(map[cipher.PubKey]bool)
	s.Blacklist = make(map[cipher.PubKey]bool)
	s.Whitelist = make(map[cipher.PubKey]bool)
	s.Rooms = make(map[cipher.PubKey]Room)
	s.Conns = make(map[cipher.PubKey]net.Conn)

	return &s, nil
}

// NewServer returns a new server
func NewServer(route util.PKRoute, info info.Info, members map[cipher.PubKey]peer.Peer, admins map[cipher.PubKey]bool, muted map[cipher.PubKey]bool, blacklist map[cipher.PubKey]bool, whitelist map[cipher.PubKey]bool, rooms map[cipher.PubKey]Room) *Server {
	s := Server{}
	s.PKRoute = route
	s.Info = info
	s.Members = members
	s.Admins = admins
	s.Muted = muted
	s.Blacklist = blacklist
	s.Whitelist = whitelist
	s.Rooms = rooms

	return &s
}

// NewDefaultServer returns a default server
func NewDefaultServer(route util.PKRoute) Server {
	s := Server{}
	s.PKRoute = route
	s.Info = info.NewDefaultInfo()

	s.Members = make(map[cipher.PubKey]peer.Peer)
	s.Admins = make(map[cipher.PubKey]bool)
	s.Muted = make(map[cipher.PubKey]bool)
	s.Blacklist = make(map[cipher.PubKey]bool)
	s.Whitelist = make(map[cipher.PubKey]bool)
	s.Rooms = make(map[cipher.PubKey]Room)
	s.Conns = make(map[cipher.PubKey]net.Conn)

	err := s.AddRoom(NewDefaultRemoteRoom(route))
	if err != nil {
		fmt.Printf("Error in adding room: %s", err)
	}

	return s
}
