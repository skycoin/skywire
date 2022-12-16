// Package userservices contains structs for the user
package userservices

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user/queries"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// Queries Contains all available query handlers of this app
type Queries struct {
	GetUserPeerBookHandler queries.GetUserPeerbookRequestHandler
	GetUserInfoHandler     queries.GetUserInfoRequestHandler
	GetUserSettingsHandler queries.GetUserSettingsRequestHandler
}

// Commands Contains all available command handlers of this app
type Commands struct {
	AddPeerHandler     commands.AddPeerRequestHandler
	DeletePeerHandler  commands.DeletePeerRequestHandler
	SetPeerHandler     commands.SetPeerRequestHandler
	SetInfoHandler     commands.SetInfoRequestHandler
	SetSettingsHandler commands.SetSettingsRequestHandler
}

// UserServices Contains the grouped queries and commands of the app layer
type UserServices struct {
	Queries  Queries
	Commands Commands
}

// NewServices Bootstraps Application Layer dependencies
func NewServices(usrRepo user.Repository, ms messenger.Service) UserServices {
	return UserServices{
		Queries: Queries{
			GetUserPeerBookHandler: queries.NewGetUserPeerbookRequestHandler(usrRepo),
			GetUserInfoHandler:     queries.NewGetUserInfoRequestHandler(usrRepo),
			GetUserSettingsHandler: queries.NewGetUserSettingsRequestHandler(usrRepo),
		},
		Commands: Commands{
			AddPeerHandler:     commands.NewAddPeerRequestHandler(usrRepo),
			DeletePeerHandler:  commands.NewDeletePeerRequestHandler(usrRepo),
			SetPeerHandler:     commands.NewSetPeerRequestHandler(usrRepo),
			SetInfoHandler:     commands.NewSetInfoRequestHandler(ms, usrRepo),
			SetSettingsHandler: commands.NewSetSettingsRequestHandler(usrRepo),
		},
	}
}
