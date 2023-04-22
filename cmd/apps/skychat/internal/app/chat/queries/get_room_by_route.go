// Package queries contains queries to get a server by pkRoute
package queries

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// GetRoomByRouteRequest Model of the Handler
type GetRoomByRouteRequest struct {
	Route util.PKRoute
}

// GetRoomByRouteResult is the result of the GetRoomByRouteRequest Query
type GetRoomByRouteResult struct {
	PKRoute util.PKRoute      // P2P: send // Server: only send when room isVisible
	Info    info.Info         // P2P: send // Server: only send when room isVisible
	Msgs    []message.Message // P2P: send // Server: only send to members when room isVisible

	IsVisible bool //setting to make room visible for all server-members
	Type      int  //roomType --> board,chat,voice

	Members   map[cipher.PubKey]peer.Peer // all members
	Mods      map[cipher.PubKey]bool      // all moderators (can mute and unmute members, can 'delete' messages, can add pks to blacklist)
	Muted     map[cipher.PubKey]bool      // all muted members (messages get received but not sent to other members)
	Blacklist map[cipher.PubKey]bool      // blacklist to block incoming connections
	Whitelist map[cipher.PubKey]bool      // maybe also a whitelist, so only specific members can connect
}

// GetRoomByRouteRequestHandler Contains the dependencies of the Handler
type GetRoomByRouteRequestHandler interface {
	Handle(query GetRoomByRouteRequest) (GetRoomByRouteResult, error)
}

type getRoomByRouteRequestHandler struct {
	visorRepo chat.Repository
}

// NewGetRoomByRouteRequestHandler Handler constructor
func NewGetRoomByRouteRequestHandler(visorRepo chat.Repository) GetRoomByRouteRequestHandler {
	return getRoomByRouteRequestHandler{visorRepo: visorRepo}
}

// Handle Handles the query
func (h getRoomByRouteRequestHandler) Handle(query GetRoomByRouteRequest) (GetRoomByRouteResult, error) {

	visor, err := h.visorRepo.GetByPK(query.Route.Visor)
	var result GetRoomByRouteResult

	if err != nil {
		return result, err
	}

	if query.Route.Visor == query.Route.Server {
		p2p, err := visor.GetP2P()
		if err != nil {
			return result, err
		}

		result = GetRoomByRouteResult{PKRoute: p2p.PKRoute, Info: p2p.Info, Msgs: p2p.Msgs, IsVisible: p2p.IsVisible, Type: p2p.Type, Members: p2p.Members, Mods: p2p.Mods, Muted: p2p.Muted, Blacklist: p2p.Blacklist, Whitelist: p2p.Whitelist}

	} else {

		server, err := visor.GetServerByPK(query.Route.Server)

		if err != nil {
			return result, err
		}

		room, err := server.GetRoomByPK(query.Route.Room)

		if err != nil {
			return result, err
		}

		result = GetRoomByRouteResult{PKRoute: room.PKRoute, Info: room.Info, Msgs: room.Msgs, IsVisible: room.IsVisible, Type: room.Type, Members: room.Members, Mods: room.Mods, Muted: room.Muted, Blacklist: server.Blacklist, Whitelist: server.Whitelist}
	}
	return result, nil
}