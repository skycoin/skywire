// Package queries contains queries to get info of a user
package queries

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// GetUserInfoResult is the result of the GetUserInfoRequest Query
type GetUserInfoResult struct {
	Pk    string
	Alias string
	Desc  string
	Img   string
}

// GetUserInfoRequestHandler Contains the dependencies of the Handler
type GetUserInfoRequestHandler interface {
	Handle() (*GetUserInfoResult, error)
}

type getUserInfoRequestHandler struct {
	usrRepo user.Repository
}

// NewGetUserInfoRequestHandler Handler constructor
func NewGetUserInfoRequestHandler(usrRepo user.Repository) GetUserInfoRequestHandler {
	return getUserInfoRequestHandler{usrRepo: usrRepo}
}

// Handle Handles the query
func (h getUserInfoRequestHandler) Handle() (*GetUserInfoResult, error) {
	usr, err := h.usrRepo.GetUser()
	var result *GetUserInfoResult
	if usr != nil && err == nil {
		i := usr.GetInfo()
		result = &GetUserInfoResult{Pk: i.GetPK().Hex(), Alias: i.GetAlias(), Desc: i.GetDescription(), Img: i.GetImg()}
	}

	return result, nil
}
