// Package chatservices contains services required by the chat app
package chatservices

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/queries"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
)

// Queries Contains all available query handlers of this app
type Queries struct {
	GetAllChatsHandler            queries.GetAllChatsRequestHandler
	GetChatByPKHandler            queries.GetChatByPKRequestHandler
	GetAllMessagesFromChatHandler queries.GetAllMessagesFromChatRequestHandler
}

// Commands Contains all available command handlers of this app
type Commands struct {
	AddChatHandler    commands.AddChatRequestHandler
	DeleteChatHandler commands.DeleteChatRequestHandler
	SendTextHandler   commands.SendTextMessageRequestHandler
}

// ChatServices Contains the grouped queries and commands of the app layer
type ChatServices struct {
	Queries  Queries
	Commands Commands
}

// NewServices Bootstraps Application Layer dependencies
func NewServices(cliRepo client.Repository, chatRepo chat.Repository, ms messenger.Service) ChatServices {
	return ChatServices{
		Queries: Queries{
			GetAllChatsHandler:            queries.NewGetAllChatsRequestHandler(chatRepo),
			GetChatByPKHandler:            queries.NewGetChatByPKRequestHandler(chatRepo),
			GetAllMessagesFromChatHandler: queries.NewGetAllMessagesFromChatRequestHandler(chatRepo),
		},
		Commands: Commands{
			AddChatHandler:    commands.NewAddChatRequestHandler(chatRepo, ms),
			DeleteChatHandler: commands.NewDeleteChatRequestHandler(cliRepo, chatRepo),
			SendTextHandler:   commands.NewSendTextMessageRequestHandler(ms),
		},
	}
}
