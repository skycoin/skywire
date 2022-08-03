package inputports

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/http"
)

//Services contains the ports services
type Services struct {
	Server *http.Server
}

//NewServices instantiates the services of input ports
func NewServices(appServices app.Services) Services {
	return Services{Server: http.NewServer(appServices)}
}
