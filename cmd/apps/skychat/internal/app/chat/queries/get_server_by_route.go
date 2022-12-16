// Package queries contains queries to get a server by pkRoute
package queries

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// GetServerByRouteRequest Model of the Handler
type GetServerByRouteRequest struct {
	Route util.PKRoute
}

// GetServerByRouteResult is the result of the GetServerByRouteRequest Query
type GetServerByRouteResult struct {
	PKRoute   util.PKRoute
	Info      info.Info                   // the public info of the server
	Members   map[cipher.PubKey]peer.Peer // all members
	Admins    map[cipher.PubKey]bool      // all admins
	Muted     map[cipher.PubKey]bool      // all members muted for all rooms
	Blacklist map[cipher.PubKey]bool      // Blacklist to block inocming connections
	Whitelist map[cipher.PubKey]bool      // maybe also a whitelist, so only specific members can connect
	Rooms     map[cipher.PubKey]chat.Room // all rooms of the server
}

// GetServerByRouteRequestHandler Contains the dependencies of the Handler
type GetServerByRouteRequestHandler interface {
	Handle(query GetServerByRouteRequest) (GetServerByRouteResult, error)
}

type getServerByRouteRequestHandler struct {
	visorRepo chat.Repository
}

// NewGetServerByRouteRequestHandler Handler constructor
func NewGetServerByRouteRequestHandler(visorRepo chat.Repository) GetServerByRouteRequestHandler {
	return getServerByRouteRequestHandler{visorRepo: visorRepo}
}

// Handle Handles the query
func (h getServerByRouteRequestHandler) Handle(query GetServerByRouteRequest) (GetServerByRouteResult, error) {

	visor, err := h.visorRepo.GetByPK(query.Route.Visor)
	var result GetServerByRouteResult

	if err != nil {
		return result, err
	}

	server, err := visor.GetServerByPK(query.Route.Server)

	if err != nil {
		return result, err
	}

	result = GetServerByRouteResult{PKRoute: server.PKRoute, Info: server.Info, Members: server.Members, Admins: server.Admins, Muted: server.Muted, Blacklist: server.Blacklist, Whitelist: server.Whitelist, Rooms: server.Rooms}

	return result, nil
}
