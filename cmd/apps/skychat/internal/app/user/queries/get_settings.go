// Package queries contains queries to get settings of a user
package queries

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// GetUserSettingsResult is the result of the GetUserSettingsRequest Query
type GetUserSettingsResult struct {
	Blacklist []string
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

		var blacklist []string
		for _, element := range s.GetBlacklist() {
			blacklist = append(blacklist, element.Hex())
		}
		if blacklist == nil {
			blacklist = []string{""}
		}

		result = &GetUserSettingsResult{Blacklist: blacklist}
	}

	return result, nil
}
