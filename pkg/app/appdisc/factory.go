// Package appdisc pkg/app/appdisc/factory.go
package appdisc

import (
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Note: HeartbeatInterval default is skyenv.ServiceDiscUpdateInterval (90 seconds)

// Factory creates appdisc.Updater instances based on the app name.
//
// The updater constructors (VisorUpdater/PublicVisorUpdater/AppUpdater) build
// service-discovery HTTP clients and so live in factory_native.go
// (//go:build !tinygo); on the TinyGo js/wasm target factory_tinygo.go hands
// out emptyUpdater. Client is typed `any` (an *http.Client on native) so this
// shared struct definition stays net/http-free.
type Factory struct {
	Log               logrus.FieldLogger
	MLog              *logging.MasterLogger
	PK                cipher.PubKey
	SK                cipher.SecKey
	ServiceDisc       string // Address of service-discovery
	DisplayNodeIP     bool
	Client            any // *http.Client on native builds
	ClientPublicIP    string
	HeartbeatInterval time.Duration // Interval for service discovery heartbeats
	Geo               any           // *geo.LocationData on native builds; visor geolocation in service entries
}

func (f *Factory) setDefaults() {
	if f.Log == nil {
		f.Log = logging.MustGetLogger("appdisc")
	}
	if f.ServiceDisc == "" {
		f.ServiceDisc = deployment.Prod.ServiceDiscovery
	}
	if f.HeartbeatInterval <= 0 {
		f.HeartbeatInterval = skyenv.ServiceDiscUpdateInterval
	}
}
