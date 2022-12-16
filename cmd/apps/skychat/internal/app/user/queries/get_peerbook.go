// Package queries contains queries to get peers of a user
package queries

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// GetUserPeerbookResult is the result of the GetUserPeerbookRequest Query
type GetUserPeerbookResult struct {
	Peerbook peer.Peerbook
}

// GetUserPeerbookRequestHandler Contains the dependencies of the Handler
type GetUserPeerbookRequestHandler interface {
	Handle() (*GetUserPeerbookResult, error)
}

type getUserPeersRequestHandler struct {
	usrRepo user.Repository
}

// NewGetUserPeerbookRequestHandler Handler constructor
func NewGetUserPeerbookRequestHandler(usrRepo user.Repository) GetUserPeerbookRequestHandler {
	return getUserPeersRequestHandler{usrRepo: usrRepo}
}

// Handle Handles the query
func (h getUserPeersRequestHandler) Handle() (*GetUserPeerbookResult, error) {
	usr, err := h.usrRepo.GetUser()
	var result *GetUserPeerbookResult

	if usr != nil && err == nil {

		result = &GetUserPeerbookResult{Peerbook: *usr.GetPeerbook()}
	}

	return result, nil
}
