// Package interfaceadapters contains the struct Services
package interfaceadapters

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/messenger/netcon"
	channel "github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/notification/http"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/storage/boltdb"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/storage/memory"
)

// InterfaceAdapterServices holds the interface adapter services as variable
var InterfaceAdapterServices Services

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
	vsrRepo := memory.NewVisorRepo()
	usrRepo := boltdb.NewUserRepo(cli.GetAppClient().Config().VisorPK)
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

// Close closes every open repository
func (s *Services) Close() error {
	err := s.ClientRepository.Close()
	if err != nil {
		fmt.Println(err)
	}
	err = s.UserRepository.Close()
	if err != nil {
		fmt.Println(err)
	}
	err = s.VisorRepository.Close()
	if err != nil {
		fmt.Println(err)
	}
	return err
}
