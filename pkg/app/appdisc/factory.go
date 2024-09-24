// Package appdisc pkg/app/appdisc/factory.go
package appdisc

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Factory creates appdisc.Updater instances based on the app name.
type Factory struct {
	Log            logrus.FieldLogger
	MLog           *logging.MasterLogger
	PK             cipher.PubKey
	SK             cipher.SecKey
	ServiceDisc    string // Address of service-discovery
	DisplayNodeIP  bool
	Client         *http.Client
	ClientPublicIP string
}

func (f *Factory) setDefaults() {
	if f.Log == nil {
		f.Log = logging.MustGetLogger("appdisc")
	}
	if f.ServiceDisc == "" {
		var envServices skywire.EnvServices
		var services skywire.Services
		var sdURL string
		if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err == nil {
			if err := json.Unmarshal(envServices.Prod, &services); err == nil {
				sdURL = services.ServiceDiscovery
			}
		}
		f.ServiceDisc = sdURL
	}
}

// VisorUpdater obtains a visor updater.
func (f *Factory) VisorUpdater(port uint16) Updater {
	// Always return empty updater if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return &emptyUpdater{}
	}

	conf := servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            f.PK,
		SK:            f.SK,
		Port:          port,
		DiscAddr:      f.ServiceDisc,
		DisplayNodeIP: f.DisplayNodeIP,
	}

	return &serviceUpdater{
		client: servicedisc.NewClient(f.Log, f.MLog, conf, f.Client, f.ClientPublicIP),
	}
}

// AppUpdater obtains an app updater based on the app name and configuration.
func (f *Factory) AppUpdater(conf appcommon.ProcConfig) (Updater, bool) {
	// Always return empty updater if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return &emptyUpdater{}, false
	}

	log := f.Log.WithField("appName", conf.AppName)

	// Do not update in service discovery if passcode-protected.
	if conf.ContainsFlag("passcode") && conf.ArgVal("passcode") != "" {
		return &emptyUpdater{}, false
	}

	getServiceDiscConf := func(conf appcommon.ProcConfig, sType string) servicedisc.Config {
		return servicedisc.Config{
			Type:     sType,
			PK:       f.PK,
			SK:       f.SK,
			Port:     uint16(conf.RoutingPort),
			DiscAddr: f.ServiceDisc,
		}
	}

	switch conf.AppName {
	case skyenv.VPNServerName:
		return &serviceUpdater{
			client: servicedisc.NewClient(log, f.MLog, getServiceDiscConf(conf, servicedisc.ServiceTypeVPN), f.Client, f.ClientPublicIP),
		}, true
	case skyenv.SkysocksName:
		return &serviceUpdater{
			client: servicedisc.NewClient(log, f.MLog, getServiceDiscConf(conf, servicedisc.ServiceTypeSkysocks), f.Client, f.ClientPublicIP),
		}, true
	default:
		return &emptyUpdater{}, false
	}
}
