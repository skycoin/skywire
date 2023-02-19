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

// types of rooms
const (
	// ErrRoomType is used to handle room errors types
	ErrRoomType = iota
	// ChatRoomType is used to define as chat room
	ChatRoomType
	// BoardRoomType is used to define as board
	//BoardRoomType
	// VoiceRoomType is used to define as voice chat
	// VoiceRoom
)

// DefaultRoomType defines the default room type
const DefaultRoomType = ChatRoomType

// Room defines a room that can be of different types
// A Room always is a part of a server
// A Server is always a part of a visor
// So you can think of this hierarchial structure:
//
//	 (Visor (PublicKey1))
//			-> P2P-Room
//			-> Server1 (PublicKey1.1)
//				-> Room1 (PublicKey1.1.1)
//				-> Room2 (PublicKey1.1.2)
//				-> Room3 (PublicKey1.1.2)
//			-> Server2 (PublicKey1.2)
//				-> Room1 (PublicKey1.2.1)
type Room struct {
	//P2P & Server
	PKRoute util.PKRoute      // P2P: send // Server: only send when room isVisible
	Info    info.Info         // P2P: send // Server: only send when room isVisible
	Msgs    []message.Message // P2P: send // Server: only send to members when room isVisible

	//Server
	IsVisible bool //setting to make room visible for all server-members
	Type      int  //roomType --> board,chat,voice

	//Private (only send to room members)
	Members   map[cipher.PubKey]peer.Peer // all members
	Mods      map[cipher.PubKey]bool      // all moderators (can mute and unmute members, can 'delete' messages, can add pks to blacklist)
	Muted     map[cipher.PubKey]bool      // all muted members (messages get received but not sent to other members)
	Blacklist map[cipher.PubKey]bool      // blacklist to block incoming connections
	Whitelist map[cipher.PubKey]bool      // maybe also a whitelist, so only specific members can connect

	//only for local server
	Conns map[cipher.PubKey]net.Conn // active peer connections
}

// GetPKRoute returns the PKRoute
func (r *Room) GetPKRoute() util.PKRoute {
	return r.PKRoute
}

// SetInfo sets the room's rinfo to the given info
func (r *Room) SetInfo(info info.Info) {
	r.Info = info
}

// GetInfo returns the info of the room
func (r *Room) GetInfo() info.Info {
	return r.Info
}

// AddMessage adds the given message to the messages
func (r *Room) AddMessage(m message.Message) {
	fmt.Printf("Added Message to route: %s \n", &r.PKRoute)
	r.Msgs = append(r.Msgs, m)
}

//[]:SetMessages to update messages

// GetMessages returns all messages
func (r *Room) GetMessages() []message.Message {
	return r.Msgs
}

// GetIsVisible returns if the room is visible
func (r *Room) GetIsVisible() bool {
	return r.IsVisible
}

// SetIsVisible sets if the room is visible
func (r *Room) SetIsVisible(isVisible bool) {
	r.IsVisible = isVisible
}

// GetType returns the room's type
func (r *Room) GetType() int {
	return r.Type
}

// SetType sets the room's type
func (r *Room) SetType(t int) {
	r.Type = t
}

// AddMember adds the given peer to the room
func (r *Room) AddMember(peer peer.Peer) error {
	_, err := r.GetMemberByPK(peer.GetPK())
	if err != nil {
		r.Members[peer.GetPK()] = peer
		return nil
	}
	return fmt.Errorf("peer already member of room")
}

// DeleteMember deletes the member with the given pk
func (r *Room) DeleteMember(pk cipher.PubKey) error {
	_, err := r.GetMemberByPK(pk)
	if err != nil {
		return nil //we don't need to send an error if the member does not even exist
	}
	delete(r.Members, pk)
	return nil
}

// GetMemberByPK returns the the member mapped by pk if available and returns err if no member with given pk exists
func (r *Room) GetMemberByPK(pk cipher.PubKey) (*peer.Peer, error) {
	if member, ok := r.Members[pk]; ok {
		return &member, nil
	}
	return nil, fmt.Errorf("member does not exist")
}

// SetMember updates the given peer
func (r *Room) SetMember(peer peer.Peer) error {
	//check if peer exists
	if _, ok := r.Members[peer.GetPK()]; ok {
		r.Members[peer.GetPK()] = peer
		return nil
	}
	return fmt.Errorf("member does not exist")
}

// GetAllMembers returns all members (peers) of the room
func (r *Room) GetAllMembers() map[cipher.PubKey]peer.Peer {
	return r.Members
}

// AddMod adds the given pk (peer) as moderator of the room
func (r *Room) AddMod(pk cipher.PubKey) error {
	//check if peer is already mod
	if _, ok := r.Mods[pk]; ok {
		return fmt.Errorf("peer is already mod")
	}
	r.Mods[pk] = true
	return nil
}

// DeleteMod removes the given pk (peer) as moderator from the room
func (r *Room) DeleteMod(pk cipher.PubKey) error {
	//check if peer is mod
	if _, ok := r.Mods[pk]; ok {
		delete(r.Mods, pk)
		return nil
	}
	return fmt.Errorf("member is no mod") //? handle as error?
}

// GetAllMods returns all moderators
func (r *Room) GetAllMods() map[cipher.PubKey]bool {
	return r.Mods
}

// AddMuted mutes the given pk (peer)
func (r *Room) AddMuted(pk cipher.PubKey) error {
	//check if peer already muted
	if _, ok := r.Muted[pk]; ok {
		return fmt.Errorf("peer already muted")
	}
	r.Muted[pk] = true //?maybe one day make map of type "Time-stamp" or something similar to enable timed mutes
	return nil
}

// DeleteMuted removes the given pk (peer) from muted status
func (r *Room) DeleteMuted(pk cipher.PubKey) error {
	//check if peer is muted
	if _, ok := r.Muted[pk]; ok {
		delete(r.Muted, pk)
		return nil
	}
	return fmt.Errorf("member is not muted") //? handle as error?
}

// GetAllMuted returns all muted members/peers
func (r *Room) GetAllMuted() map[cipher.PubKey]bool {
	return r.Muted
}

// AddToBlacklist blocks the given pk from joining the room
func (r *Room) AddToBlacklist(pk cipher.PubKey) error {
	//check if peer already blacklisted
	if _, ok := r.Blacklist[pk]; ok {
		return fmt.Errorf("peer already blacklisted")
	}
	r.Blacklist[pk] = true
	return nil
}

// DeleteFromBlacklist unblocks the given pk from joining the room
func (r *Room) DeleteFromBlacklist(pk cipher.PubKey) error {
	//check if peer is blacklisted
	if _, ok := r.Blacklist[pk]; ok {
		delete(r.Blacklist, pk)
		return nil
	}
	return fmt.Errorf("member is not blacklisted") //? handle as error?
}

// GetBlacklist returns all blacklisted/banned members/peers
func (r *Room) GetBlacklist() map[cipher.PubKey]bool {
	return r.Blacklist
}

// AddToWhitelist adds a pk to the join-only-list of the room
func (r *Room) AddToWhitelist(pk cipher.PubKey) error {
	//check if peer already whitelisted
	if _, ok := r.Whitelist[pk]; ok {
		return fmt.Errorf("peer already whitelisted")
	}
	r.Whitelist[pk] = true
	return nil
}

// DeleteFromWhitelist removes a pk from the join-only-list of the room
func (r *Room) DeleteFromWhitelist(pk cipher.PubKey) error {
	//check if peer is whitelisted
	if _, ok := r.Whitelist[pk]; ok {
		delete(r.Whitelist, pk)
		return nil
	}
	return fmt.Errorf("member is not whitelisted") //? handle as error?
}

// GetWhitelist returns all whitelisted members/peers
func (r *Room) GetWhitelist(pk cipher.PubKey) map[cipher.PubKey]bool {
	return r.Whitelist
}

// NewDefaultLocalRoom returns a new default local room
func NewDefaultLocalRoom(roomRoute util.PKRoute) Room {
	r := Room{}
	r.PKRoute = roomRoute
	r.Info = info.NewDefaultInfo()
	//Msgs
	r.IsVisible = false
	r.Type = ChatRoomType

	r.Members = make(map[cipher.PubKey]peer.Peer)
	r.Mods = make(map[cipher.PubKey]bool)
	r.Muted = make(map[cipher.PubKey]bool)
	r.Blacklist = make(map[cipher.PubKey]bool)
	r.Whitelist = make(map[cipher.PubKey]bool)
	r.Conns = make(map[cipher.PubKey]net.Conn)

	return r
}

// NewDefaultRemoteRoom returns a new default remote room
func NewDefaultRemoteRoom(roomRoute util.PKRoute) Room {
	r := Room{}
	r.PKRoute = roomRoute
	r.Info = info.NewDefaultInfo()
	//Msgs
	r.IsVisible = true
	r.Type = ChatRoomType

	r.Members = make(map[cipher.PubKey]peer.Peer)
	r.Mods = make(map[cipher.PubKey]bool)
	r.Muted = make(map[cipher.PubKey]bool)
	r.Blacklist = make(map[cipher.PubKey]bool)
	r.Whitelist = make(map[cipher.PubKey]bool)
	r.Conns = make(map[cipher.PubKey]net.Conn)

	return r
}

// NewDefaultP2PRoom returns a new default p2p room
func NewDefaultP2PRoom(pk cipher.PubKey) Room {
	r := Room{}
	r.PKRoute = util.NewP2PRoute(pk)
	r.Info = info.NewDefaultInfo()
	r.IsVisible = true
	r.Type = ChatRoomType

	r.Members = make(map[cipher.PubKey]peer.Peer)
	r.Mods = make(map[cipher.PubKey]bool)
	r.Muted = make(map[cipher.PubKey]bool)
	r.Blacklist = make(map[cipher.PubKey]bool)
	r.Whitelist = make(map[cipher.PubKey]bool)
	r.Conns = make(map[cipher.PubKey]net.Conn)

	return r
}

// NewLocalRoom returns a new local room
func NewLocalRoom(roomRoute util.PKRoute, i info.Info, t int) Room {
	r := Room{}
	r.PKRoute = roomRoute
	r.Info = i
	//[]:maybe delete when a picture can be set from the ui?
	if i.Img == "" {
		r.Info.Img = info.DefaultImage
	}
	r.Info.Pk = roomRoute.Room
	//Msgs
	r.IsVisible = false
	r.Type = t

	r.Members = make(map[cipher.PubKey]peer.Peer)
	r.Mods = make(map[cipher.PubKey]bool)
	r.Muted = make(map[cipher.PubKey]bool)
	r.Blacklist = make(map[cipher.PubKey]bool)
	r.Whitelist = make(map[cipher.PubKey]bool)
	r.Conns = make(map[cipher.PubKey]net.Conn)

	return r
}
