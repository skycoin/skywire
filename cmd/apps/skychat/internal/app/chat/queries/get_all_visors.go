// Package queries contains queries to get all visors
package queries

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
)

// GetAllVisorsResult is the result of the GetAllVisorsRequest Query
type GetAllVisorsResult struct {
	Pk     cipher.PubKey
	P2P    chat.Room
	Server map[cipher.PubKey]chat.Server
}

// GetAllVisorsRequestHandler Contains the dependencies of the Handler
type GetAllVisorsRequestHandler interface {
	Handle() ([]GetAllVisorsResult, error)
}

type getAllVisorsRequestHandler struct {
	visorRepo chat.Repository
}

// NewGetAllVisorsRequestHandler Handler constructor
func NewGetAllVisorsRequestHandler(visorRepo chat.Repository) GetAllVisorsRequestHandler {
	return getAllVisorsRequestHandler{visorRepo: visorRepo}
}

// Handle Handles the query
func (h getAllVisorsRequestHandler) Handle() ([]GetAllVisorsResult, error) {

	res, err := h.visorRepo.GetAll()
	if err != nil {
		return nil, err
	}
	var result []GetAllVisorsResult
	for _, visor := range res {
		p2p, err := visor.GetP2P()
		if err != nil {
			return nil, err
		}
		result = append(result, GetAllVisorsResult{Pk: visor.GetPK(), P2P: p2p, Server: visor.GetAllServer()})
	}
	return result, nil
}
