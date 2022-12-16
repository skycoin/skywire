// Package queries contains queries to get chat by pk
package queries

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
)

// GetVisorByPKRequest Model of the Handler
type GetVisorByPKRequest struct {
	Pk cipher.PubKey
}

// GetVisorByPKResult is the result of the GetVisorByPKRequest Query
type GetVisorByPKResult struct {
	Pk     cipher.PubKey
	P2P    chat.Room
	Server map[cipher.PubKey]chat.Server
}

// GetVisorByPKRequestHandler Contains the dependencies of the Handler
type GetVisorByPKRequestHandler interface {
	Handle(query GetVisorByPKRequest) (GetVisorByPKResult, error)
}

type getVisorByPKRequestHandler struct {
	visorRepo chat.Repository
}

// NewGetVisorByPKRequestHandler Handler constructor
func NewGetVisorByPKRequestHandler(visorRepo chat.Repository) GetVisorByPKRequestHandler {
	return getVisorByPKRequestHandler{visorRepo: visorRepo}
}

// Handle Handles the query
func (h getVisorByPKRequestHandler) Handle(query GetVisorByPKRequest) (GetVisorByPKResult, error) {

	var result GetVisorByPKResult

	visor, err := h.visorRepo.GetByPK(query.Pk)
	if err != nil {
		return result, err
	}

	p2p, err := visor.GetP2P()
	if err != nil {
		return GetVisorByPKResult{}, err
	}

	result = GetVisorByPKResult{Pk: visor.GetPK(), P2P: p2p, Server: visor.GetAllServer()}

	return result, nil
}
