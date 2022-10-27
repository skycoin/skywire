// Package userservices contains structs for the user
package userservices

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user/queries"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// Queries Contains all available query handlers of this app
type Queries struct {
	GetUserInfoHandler     queries.GetUserInfoRequestHandler
	GetUserSettingsHandler queries.GetUserSettingsRequestHandler
}

// Commands Contains all available command handlers of this app
type Commands struct {
	SetInfoHandler     commands.SetInfoRequestHandler
	SetSettingsHandler commands.SetSettingsRequestHandler
}

// UserServices Contains the grouped queries and commands of the app layer
type UserServices struct {
	Queries  Queries
	Commands Commands
}

// NewServices Bootstraps Application Layer dependencies
func NewServices(cliRepo user.Repository, chatRepo chat.Repository) UserServices {
	return UserServices{
		Queries: Queries{
			GetUserInfoHandler:     queries.NewGetUserInfoRequestHandler(cliRepo),
			GetUserSettingsHandler: queries.NewGetUserSettingsRequestHandler(cliRepo),
		},
		Commands: Commands{
			SetInfoHandler:     commands.NewSetInfoRequestHandler(cliRepo),
			SetSettingsHandler: commands.NewSetSettingsRequestHandler(cliRepo),
		},
	}
}
