// Package queries contains queries to get settings of a user
package queries

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// GetUserSettingsResult is the result of the GetUserSettingsRequest Query
type GetUserSettingsResult struct {
	Blacklist []cipher.PubKey
}

// GetUserSettingsRequestHandler Contains the dependencies of the Handler
type GetUserSettingsRequestHandler interface {
	Handle() (*GetUserSettingsResult, error)
}

type getUserSettingsRequestHandler struct {
	usrRepo user.Repository
}

// NewGetUserSettingsRequestHandler Handler constructor
func NewGetUserSettingsRequestHandler(usrRepo user.Repository) GetUserSettingsRequestHandler {
	return getUserSettingsRequestHandler{usrRepo: usrRepo}
}

// Handle Handles the query
func (h getUserSettingsRequestHandler) Handle() (*GetUserSettingsResult, error) {
	usr, err := h.usrRepo.GetUser()
	var result *GetUserSettingsResult

	if usr != nil && err == nil {
		s := usr.GetSettings()

		result = &GetUserSettingsResult{Blacklist: s.GetBlacklist()}
	}

	return result, nil
}
