package visor

import (
	"github.com/SkycoinProject/dmsg/cipher"
)

// TODO: Remove.
func baseConfig() (*Config, error) {
	conf := &Config{}

	conf.Visor = NewKeyPair()

	c, err := DefaultSTCPConfig()
	if err != nil {
		return nil, err
	}

	conf.STCP = c

	conf.Dmsg = DefaultDmsgConfig()

	conf.DmsgPty = DefaultDmsgPtyConfig()

	conf.TrustedVisors = []cipher.PubKey{}

	conf.Transport = DefaultTransportConfig()

	conf.Routing = DefaultRoutingConfig()

	conf.Hypervisors = []HypervisorConfig{}

	conf.UptimeTracker = DefaultUptimeTrackerConfig()

	conf.AppsPath = "./apps"
	conf.LocalPath = DefaultLocalPath

	conf.LogLevel = "info"

	conf.ShutdownTimeout = DefaultTimeout

	conf.Interfaces = DefaultInterfaceConfig()

	conf.AppServerSockFile = "/tmp/visor_" + conf.Keys().StaticPubKey.Hex() + ".sock"

	return conf, nil
}
