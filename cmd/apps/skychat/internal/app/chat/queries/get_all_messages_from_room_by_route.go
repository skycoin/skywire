// Package queries contains queries to get all messages from a room
package queries

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// GetAllMessagesFromRoomRequest Model of the Handler
type GetAllMessagesFromRoomRequest struct {
	Route util.PKRoute
}

// GetAllMessagesFromRoomResult is the return model of Chat Query Handlers
type GetAllMessagesFromRoomResult struct {
	Messages []message.Message
}

// GetAllMessagesFromRoomRequestHandler provides an interfaces to handle a GetAllMessagesFromRoomRequest and return a *GetAllMessagesFromRoomResult
type GetAllMessagesFromRoomRequestHandler interface {
	Handle(query GetAllMessagesFromRoomRequest) (GetAllMessagesFromRoomResult, error)
}

type getAllMessagesFromRoomRequestHandler struct {
	visorRepo chat.Repository
}

// NewGetAllMessagesFromRoomRequestHandler Handler Constructor
func NewGetAllMessagesFromRoomRequestHandler(visorRepo chat.Repository) GetAllMessagesFromRoomRequestHandler {
	return getAllMessagesFromRoomRequestHandler{visorRepo: visorRepo}
}

// Handle Handlers the GetAllMessagesFromRoomRequest query
func (h getAllMessagesFromRoomRequestHandler) Handle(query GetAllMessagesFromRoomRequest) (GetAllMessagesFromRoomResult, error) {
	var result GetAllMessagesFromRoomResult

	visor, err := h.visorRepo.GetByPK(query.Route.Visor)
	if err != nil {
		return result, err
	}
	var msgs []message.Message

	if query.Route.Server == query.Route.Visor {
		p2p, err := visor.GetP2P()
		if err != nil {
			return result, err
		}
		msgs = p2p.GetMessages()
	} else {
		server, err := visor.GetServerByPK(query.Route.Server)
		if err != nil {
			return result, err
		}
		room, err := server.GetRoomByPK(query.Route.Room)
		if err != nil {
			return result, err
		}
		msgs = room.GetMessages()
	}

	if msgs != nil {
		result = GetAllMessagesFromRoomResult{Messages: msgs}
	}
	return result, nil
}
