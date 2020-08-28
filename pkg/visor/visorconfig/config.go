package visorconfig

import (
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
)

// MakeBaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
// This function always returns the latest config version.
func MakeBaseConfig(common *Common) *V1 {
	conf := new(V1)
	conf.Common = common
	conf.Dmsg = &snet.DmsgConfig{
		Discovery:     skyenv.DefaultDmsgDiscAddr,
		SessionsCount: 1,
	}
	conf.Transport = &V1Transport{
		Discovery:       skyenv.DefaultTpDiscAddr,
		AddressResolver: skyenv.DefaultAddressResolverAddr,
		LogStore: &V1LogStore{
			Type: "memory",
		},
	}
	conf.Routing = &V1Routing{
		SetupNodes:         []cipher.PubKey{skyenv.MustPK(skyenv.DefaultSetupPK)},
		RouteFinder:        skyenv.DefaultRouteFinderAddr,
		RouteFinderTimeout: DefaultTimeout,
	}
	conf.Launcher = &V1Launcher{
		Discovery:  nil,
		Apps:       nil,
		ServerAddr: skyenv.DefaultAppSrvAddr,
		BinPath:    skyenv.DefaultAppBinPath,
		LocalPath:  skyenv.DefaultAppLocalPath,
	}
	conf.CLIAddr = skyenv.DefaultRPCAddr
	conf.LogLevel = skyenv.DefaultLogLevel
	conf.ShutdownTimeout = DefaultTimeout
	conf.RestartCheckDelay = restart.DefaultCheckDelay.String() // TODO: Use Duration type.
	return conf
}

// MakeDefaultConfig returns the default visor config from a given secret key (if specified).
// The config's 'sk' field will be nil if not specified.
// Generated config will be saved to 'confPath'.
// This function always returns the latest config version.
func MakeDefaultConfig(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool) (*V1, error) {
	cc, err := NewCommon(log, confPath, V1Name, sk)
	if err != nil {
		return nil, err
	}
	return defaultConfigFromCommon(cc, hypervisor)
}

func defaultConfigFromCommon(cc *Common, hypervisor bool) (*V1, error) {
	// Enforce version and keys in 'cc'.
	cc.Version = V1Name
	if err := cc.ensureKeys(); err != nil {
		return nil, err
	}

	// Actual config generation.
	conf := MakeBaseConfig(cc)

	conf.Dmsgpty = &V1Dmsgpty{
		Port:     skyenv.DmsgPtyPort,
		AuthFile: skyenv.DefaultDmsgPtyWhitelist,
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}

	conf.STCP = &snet.STCPConfig{
		LocalAddr: skyenv.DefaultSTCPAddr,
		PKTable:   nil,
	}

	conf.Transport.LogStore = &V1LogStore{
		Type:     "file",
		Location: skyenv.DefaultTpLogStore,
	}

	conf.UptimeTracker = &V1UptimeTracker{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}

	conf.Launcher.Discovery = &V1AppDisc{
		UpdateInterval: Duration(skyenv.AppDiscUpdateInterval),
		ServiceDisc:    skyenv.DefaultServiceDiscAddr,
	}

	conf.Launcher.Apps = []launcher.AppConfig{
		{
			Name:      skyenv.SkychatName,
			AutoStart: true,
			Port:      routing.Port(skyenv.SkychatPort),
			Args:      []string{"-addr", skyenv.SkychatAddr},
		},
		{
			Name:      skyenv.SkysocksName,
			AutoStart: true,
			Port:      routing.Port(skyenv.SkysocksPort),
		},
		{
			Name:      skyenv.SkysocksClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.SkysocksClientPort),
		},
		{
			Name:      skyenv.VPNServerName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNServerPort),
		},
		{
			Name:      skyenv.VPNClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
		},
	}

	conf.Hypervisors = make([]cipher.PubKey, 0)

	if hypervisor {
		config := hypervisorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}

	return conf, nil
}

// MakeTestConfig acts like MakeDefaultConfig, however, test deployment service addresses are used instead.
func MakeTestConfig(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool) (*V1, error) {
	conf, err := MakeDefaultConfig(log, confPath, sk, hypervisor)
	if err != nil {
		return nil, err
	}

	conf.Dmsg.Discovery = skyenv.TestDmsgDiscAddr
	conf.Transport.Discovery = skyenv.TestTpDiscAddr
	conf.Transport.AddressResolver = skyenv.TestAddressResolverAddr
	conf.Routing.RouteFinder = skyenv.TestRouteFinderAddr
	conf.Routing.SetupNodes = []cipher.PubKey{skyenv.MustPK(skyenv.TestSetupPK)}
	conf.UptimeTracker.Addr = skyenv.TestUptimeTrackerAddr
	conf.Launcher.Discovery.ServiceDisc = skyenv.TestServiceDiscAddr
	if conf.Hypervisor != nil {
		conf.Hypervisor.DmsgDiscovery = conf.Transport.Discovery
	}

	return conf, nil
}
