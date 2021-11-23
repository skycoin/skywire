package visorconfig

import (
	"runtime"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
)

// MakeBaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
// This function always returns the latest config version.
func MakeBaseConfig(common *Common) *V1 {
	conf := new(V1)
	conf.Common = common
	conf.Dmsg = &dmsgc.DmsgConfig{
		Discovery:     skyenv.DefaultDmsgDiscAddr,
		SessionsCount: 1,
		Servers:       []string{},
	}
	conf.Transport = &V1Transport{
		Discovery:         skyenv.DefaultTpDiscAddr,
		AddressResolver:   skyenv.DefaultAddressResolverAddr,
		PublicAutoconnect: true,
	}
	conf.Routing = &V1Routing{
		SetupNodes:         []cipher.PubKey{skyenv.MustPK(skyenv.DefaultSetupPK)},
		RouteFinder:        skyenv.DefaultRouteFinderAddr,
		RouteFinderTimeout: DefaultTimeout,
	}
	conf.Launcher = &V1Launcher{
		ServiceDisc: skyenv.DefaultServiceDiscAddr,
		Apps:        nil,
		ServerAddr:  skyenv.DefaultAppSrvAddr,
		BinPath:     skyenv.DefaultAppBinPath,
	}
	conf.UptimeTracker = &V1UptimeTracker{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}
	conf.CLIAddr = skyenv.DefaultRPCAddr
	conf.LogLevel = skyenv.DefaultLogLevel
	conf.LocalPath = skyenv.DefaultLocalPath
	conf.StunServers = skyenv.GetStunServers()
	conf.ShutdownTimeout = DefaultTimeout
	conf.RestartCheckDelay = Duration(restart.DefaultCheckDelay)
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
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}

	conf.STCP = &network.STCPConfig{
		ListeningAddress: skyenv.DefaultSTCPAddr,
		PKTable:          nil,
	}

	conf.UptimeTracker = &V1UptimeTracker{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}

	conf.Launcher.ServiceDisc = skyenv.DefaultServiceDiscAddr

	conf.Launcher.Apps = makeDefaultLauncherAppsConfig()

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
	SetDefaultTestingValues(conf)
	if conf.Hypervisor != nil {
		conf.Hypervisor.DmsgDiscovery = conf.Transport.Discovery
	}

	return conf, nil
}

// MakePackageConfig acts like MakeDefaultConfig but use package config defaults
func MakePackageConfig(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool) (*V1, error) {
	conf, err := MakeDefaultConfig(log, confPath, sk, hypervisor)
	if err != nil {
		return nil, err
	}

	conf.Dmsgpty = &V1Dmsgpty{
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}
	conf.LocalPath = skyenv.PackageAppLocalPath()
	conf.Launcher.BinPath = skyenv.PackageAppBinPath()

	if conf.Hypervisor != nil {
		conf.Hypervisor.EnableAuth = skyenv.DefaultPackageEnableAuth
		conf.Hypervisor.TLSKeyFile = skyenv.PackageTLSKey()
		conf.Hypervisor.TLSCertFile = skyenv.PackageTLSCert()
		conf.Hypervisor.TLSKeyFile = skyenv.PackageTLSKey()
		conf.Hypervisor.TLSCertFile = skyenv.PackageTLSCert()
		conf.Hypervisor.DBPath = skyenv.PackageDBPath()
	}
	return conf, nil
}

// MakeSkybianConfig acts like MakeDefaultConfig but uses default paths, etc. as found in skybian / produced by skyimager
func MakeSkybianConfig(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool) (*V1, error) {
	conf, err := MakeDefaultConfig(log, confPath, sk, hypervisor)
	if err != nil {
		return nil, err
	}

	conf.Dmsgpty = &V1Dmsgpty{
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.SkybianDmsgPtyCLIAddr,
	}
	conf.LocalPath = skyenv.SkybianLocalPath
	conf.Launcher.BinPath = skyenv.SkybianAppBinPath

	if conf.Hypervisor != nil {
		conf.Hypervisor.EnableAuth = skyenv.DefaultEnableAuth
		conf.Hypervisor.EnableTLS = skyenv.SkybianEnableTLS
		conf.Hypervisor.TLSKeyFile = skyenv.SkybianTLSKey
		conf.Hypervisor.TLSCertFile = skyenv.SkybianTLSCert
		conf.Hypervisor.DBPath = skyenv.SkybianDBPath
	}
	return conf, nil
}

// SetDefaultTestingValues mutates configuration to use testing values
func SetDefaultTestingValues(conf *V1) {
	conf.Dmsg.Discovery = skyenv.TestDmsgDiscAddr
	conf.Transport.Discovery = skyenv.TestTpDiscAddr
	conf.Transport.AddressResolver = skyenv.TestAddressResolverAddr
	conf.Routing.RouteFinder = skyenv.TestRouteFinderAddr
	conf.Routing.SetupNodes = []cipher.PubKey{skyenv.MustPK(skyenv.TestSetupPK)}
	conf.UptimeTracker.Addr = skyenv.TestUptimeTrackerAddr
	conf.Launcher.ServiceDisc = skyenv.TestServiceDiscAddr
}

// SetDefaultProductionValues mutates configuration to use production values
func SetDefaultProductionValues(conf *V1) {
	conf.Dmsg.Discovery = skyenv.DefaultDmsgDiscAddr
	conf.Transport.Discovery = skyenv.DefaultTpDiscAddr
	conf.Transport.AddressResolver = skyenv.DefaultAddressResolverAddr
	conf.Routing.RouteFinder = skyenv.DefaultRouteFinderAddr
	conf.Routing.SetupNodes = []cipher.PubKey{skyenv.MustPK(skyenv.DefaultSetupPK)}
	conf.UptimeTracker = &V1UptimeTracker{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}
	conf.Launcher.ServiceDisc = skyenv.DefaultServiceDiscAddr
}

// makeDefaultLauncherAppsConfig creates default launcher config for apps,
// for package based installation in other platform (Darwin, Windows) it only includes
// the shipped apps for that platforms
func makeDefaultLauncherAppsConfig() []launcher.AppConfig {
	defaultConfig := []launcher.AppConfig{
		{
			Name:      skyenv.VPNClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
		},
	}

	switch runtime.GOOS {
	case "linux":
		return launcherAddAllApps(defaultConfig)
	case "darwin":
		return defaultConfig
	case "windows":
		return defaultConfig
	default:
		return defaultConfig
	}
}

func launcherAddAllApps(launcherCfg []launcher.AppConfig) []launcher.AppConfig {
	launcherCfg = append(launcherCfg, []launcher.AppConfig{
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
	}...)
	return launcherCfg
}

// DmsgHTTPServers struct use to unmarshal dmsghttp file
type DmsgHTTPServers struct {
	Test DmsgHTTPServersData `json:"test"`
	Prod DmsgHTTPServersData `json:"prod"`
}

// DmsgHTTPServersData is a part of DmsgHTTPServers
type DmsgHTTPServersData struct {
	DMSGServers        []string `json:"dmsg_servers"`
	DMSGDiscovery      string   `json:"dmsg_discovery"`
	TransportDiscovery string   `json:"transport_discovery"`
	AddressResolver    string   `json:"address_resolver"`
	RouteFinder        string   `json:"route_finder"`
	UptimeTracker      string   `json:"uptime_tracker"`
	ServiceDiscovery   string   `json:"service_discovery"`
}
