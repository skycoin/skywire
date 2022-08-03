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

//Services contains the exposed services of interface adapters
type Services struct {
	ClientRepository    client.ClientRepository
	UserRepository      user.UserRepository
	ChatRepository      chat.ChatRepository
	MessengerService    messenger.Service
	NotificationService notification.Service
}

//NewServices Instantiates the interface adapter services
func NewServices() Services {
	cliRepo := memory.NewClientRepo()
	cli, _ := cliRepo.GetClient()
	usrRepo := memory.NewUserRepo(cli.GetAppClient().Config().VisorPK)
	chtRepo := memory.NewChatRepo() //memory.NewDummyChatRepo(cli.GetAppClient().Config().VisorPK)
	ns := channel.NewNotificationService()

	return Services{
		ClientRepository:    cliRepo,
		UserRepository:      usrRepo,
		ChatRepository:      chtRepo,
		MessengerService:    netcon.NewMessengerService(ns, cliRepo, usrRepo, chtRepo),
		NotificationService: ns,
	}
}
