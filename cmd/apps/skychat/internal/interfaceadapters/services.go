// Package interfaceadapters contains the struct Services
package interfaceadapters

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/connectionhandler"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/messenger"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/client"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/connectionhandler/netcon"
	messengerimpl "github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/messenger"
	channel "github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/notification/http"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/storage/boltdb"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters/storage/memory"
)

// InterfaceAdapterServices holds the interface adapter services as variable
var InterfaceAdapterServices Services

// Services contains the exposed services of interface adapters
type Services struct {
	ClientRepository         client.Repository
	UserRepository           user.Repository
	VisorRepository          chat.Repository
	MessengerService         messenger.Service
	ConnectionHandlerService connectionhandler.Service
	NotificationService      notification.Service
}

// NewServices Instantiates the interface adapter services
func NewServices() Services {
	cliRepo := memory.NewClientRepo()
	cli, _ := cliRepo.GetClient() //nolint
	usrRepo := boltdb.NewUserRepo(cli.GetAppClient().Config().VisorPK)
	vsrRepo := boltdb.NewVisorRepo()
	ns := channel.NewNotificationService()

	//channel is for communication between the next two services, that otherwise would be dependent on each other
	messagesReceived := make(chan message.Message)
	ch := netcon.NewConnectionHandlerService(ns, cliRepo, vsrRepo, messagesReceived)
	ms := messengerimpl.NewMessengerService(ns, cliRepo, usrRepo, vsrRepo, ch)

	return Services{
		ClientRepository:         cliRepo,
		UserRepository:           usrRepo,
		VisorRepository:          vsrRepo,
		MessengerService:         ms,
		ConnectionHandlerService: ch,
		NotificationService:      ns,
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
