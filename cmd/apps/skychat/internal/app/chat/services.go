// Package chatservices contains services required by the chat app
package chatservices

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/commands"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat/queries"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// Queries Contains all available query handlers of this app
type Queries struct {
	GetRoomByRouteHandler         queries.GetRoomByRouteRequestHandler
	GetServerByRouteHandler       queries.GetServerByRouteRequestHandler
	GetAllVisorsHandler           queries.GetAllVisorsRequestHandler
	GetVisorByPKHandler           queries.GetVisorByPKRequestHandler
	GetAllMessagesFromRoomHandler queries.GetAllMessagesFromRoomRequestHandler
}

// Commands Contains all available command handlers of this app
type Commands struct {
	AddLocalServerHandler        commands.AddLocalServerRequestHandler
	JoinRemoteRouteHandler       commands.JoinRemoteRouteRequestHandler
	DeleteLocalRouteHandler      commands.DeleteLocalRouteRequestHandler
	LeaveRemoteRouteHandler      commands.LeaveRemoteRouteRequestHandler
	SendAddRoomMessageHandler    commands.SendAddRoomMessageRequestHandler
	SendDeleteRoomMessageHandler commands.SendDeleteRoomMessageRequestHandler
	SendTextMessageHandler       commands.SendTextMessageRequestHandler
	SendMutePeerMessageHandler   commands.SendMutePeerMessageRequestHandler
	SendUnmutePeerMessageHandler commands.SendUnmutePeerMessageRequestHandler
}

// ChatServices Contains the grouped queries and commands of the app layer
type ChatServices struct {
	Queries  Queries
	Commands Commands
}

// NewServices Bootstraps Application Layer dependencies
func NewServices(cliRepo client.Repository, visorRepo chat.Repository, userRepo user.Repository, ms messenger.Service, ns notification.Service) ChatServices {
	return ChatServices{
		Queries: Queries{
			GetRoomByRouteHandler:         queries.NewGetRoomByRouteRequestHandler(visorRepo),
			GetServerByRouteHandler:       queries.NewGetServerByRouteRequestHandler(visorRepo),
			GetAllVisorsHandler:           queries.NewGetAllVisorsRequestHandler(visorRepo),
			GetVisorByPKHandler:           queries.NewGetVisorByPKRequestHandler(visorRepo),
			GetAllMessagesFromRoomHandler: queries.NewGetAllMessagesFromRoomRequestHandler(visorRepo),
		},
		Commands: Commands{
			AddLocalServerHandler:        commands.NewAddLocalServerRequestHandler(visorRepo, userRepo, ns),
			JoinRemoteRouteHandler:       commands.NewJoinRemoteRouteRequestHandler(visorRepo, ms),
			DeleteLocalRouteHandler:      commands.NewDeleteLocalRouteRequestHandler(ms, visorRepo),
			LeaveRemoteRouteHandler:      commands.NewLeaveRemoteRouteRequestHandler(ms, visorRepo, userRepo),
			SendAddRoomMessageHandler:    commands.NewSendAddRoomMessageRequestHandler(ms),
			SendDeleteRoomMessageHandler: commands.NewSendDeleteRoomMessageRequestHandler(ms),
			SendTextMessageHandler:       commands.NewSendTextMessageRequestHandler(ms),
			SendMutePeerMessageHandler:   commands.NewSendMutePeerMessageRequestHandler(ms),
			SendUnmutePeerMessageHandler: commands.NewSendUnmutePeerMessageRequestHandler(ms),
		},
	}
}
