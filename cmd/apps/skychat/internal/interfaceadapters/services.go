// Package interfaceadapters contains the struct Services
package interfaceadapters

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/messenger/netcon"
	channel "github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/notification/http"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/storage/memory"
)

// Services contains the exposed services of interface adapters
type Services struct {
	ClientRepository    client.Repository
	UserRepository      user.Repository
	VisorRepository     chat.Repository
	MessengerService    messenger.Service
	NotificationService notification.Service
}

// NewServices Instantiates the interface adapter services
func NewServices() Services {
	cliRepo := memory.NewClientRepo()
	cli, _ := cliRepo.GetClient() //nolint
	usrRepo := memory.NewUserRepo(cli.GetAppClient().Config().VisorPK)
	vsrRepo := memory.NewVisorRepo()
	ns := channel.NewNotificationService()
	ms := netcon.NewMessengerService(ns, cliRepo, usrRepo, vsrRepo)

	return Services{
		ClientRepository:    cliRepo,
		UserRepository:      usrRepo,
		VisorRepository:     vsrRepo,
		MessengerService:    ms,
		NotificationService: ns,
	}
}
