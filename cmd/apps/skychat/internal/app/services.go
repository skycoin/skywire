// Package app contains the Services struct for the app
package app

import (
	chatservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	userservices "github.com/skycoin/skywire/cmd/apps/skychat/internal/app/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// Services contains all exposed services of the application layer
type Services struct {
	NotificationService notification.Service
	ChatServices        chatservices.ChatServices
	UserServices        userservices.UserServices
}

// NewServices Bootstraps Application Layer dependencies
func NewServices(cliRepo client.Repository, usrRepo user.Repository, visorRepo chat.Repository, notifyService notification.Service, ms messenger.Service) Services {
	return Services{
		NotificationService: notifyService,
		ChatServices:        chatservices.NewServices(cliRepo, visorRepo, usrRepo, ms, notifyService),
		UserServices:        userservices.NewServices(usrRepo, ms)}
}
