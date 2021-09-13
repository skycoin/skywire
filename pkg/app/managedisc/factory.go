package managedisc

import (
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Factory creates appdisc.Updater instances based on the app name.
type Factory struct {
	Log         logrus.FieldLogger
	PK          cipher.PubKey
	SK          cipher.SecKey
	ServiceDisc string // Address of service-discovery
}

func (f *Factory) setDefaults() {
	if f.Log == nil {
		f.Log = logging.MustGetLogger("appdisc")
	}
	if f.ServiceDisc == "" {
		f.ServiceDisc = skyenv.DefaultServiceDiscAddr
	}
}

// VisorManager obtains a visor manager.
func (f *Factory) VisorManager(port uint16) Manager {
	// Always return empty manager if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return &emptyManager{}
	}

	conf := servicedisc.Config{
		Type:     servicedisc.ServiceTypeVisor,
		PK:       f.PK,
		SK:       f.SK,
		Port:     port,
		DiscAddr: f.ServiceDisc,
	}

	return &serviceManager{
		client: servicedisc.NewClient(f.Log, conf),
	}
}

// AppManager obtains an app manager based on the app name and configuration.
func (f *Factory) AppManager(conf appcommon.ProcConfig) (Manager, bool) {
	// Always return empty manager if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return &emptyManager{}, false
	}

	log := f.Log.WithField("appName", conf.AppName)

	// Do not manage in service discovery if passcode-protected.
	if conf.ContainsFlag("passcode") && conf.ArgVal("passcode") != "" {
		return &emptyManager{}, false
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
		return &serviceManager{
			client: servicedisc.NewClient(log, getServiceDiscConf(conf, servicedisc.ServiceTypeVPN)),
		}, true
	default:
		return &emptyManager{}, false
	}
}
