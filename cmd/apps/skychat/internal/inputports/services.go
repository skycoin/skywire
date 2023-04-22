// Package inputports contains Services struct
package inputports

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/http"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/rpc"
)

// InputportsServices holds the inputports services as variable
var InputportsServices Services

// Services contains the ports services
type Services struct {
	HTTPServer *http.Server
	RPCServer  *rpc.Server
	RPCClient  *rpc.Client
}

// NewServices instantiates the services of input ports
func NewServices(appServices app.Services) Services {
	return Services{
		HTTPServer: http.NewServer(appServices),
		RPCServer:  rpc.NewServer(appServices),
		RPCClient:  rpc.NewClient(appServices),
	}
}
