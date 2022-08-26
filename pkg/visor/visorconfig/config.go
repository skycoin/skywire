package visorconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/dmsg/pkg/disc"
	coinCipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/appserver"
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
func MakeBaseConfig(common *Common, testEnv bool, dmsgHTTP bool, services *Services, dmsgHTTPServersList *DmsgHTTPServers) *V1 {
	//check if any services were passed
	if services == nil {
		//fall back on skyev defaults
		if !testEnv {
			services = &Services{utilenv.DmsgDiscAddr, utilenv.TpDiscAddr, utilenv.AddressResolverAddr, utilenv.RouteFinderAddr, []cipher.PubKey{skyenv.MustPK(utilenv.SetupPK)}, utilenv.UptimeTrackerAddr, utilenv.ServiceDiscAddr, utilenv.GetStunServers()}
		} else {
			services = &Services{utilenv.TestDmsgDiscAddr, utilenv.TestTpDiscAddr, utilenv.TestAddressResolverAddr, utilenv.TestRouteFinderAddr, []cipher.PubKey{skyenv.MustPK(utilenv.TestSetupPK)}, utilenv.TestUptimeTrackerAddr, utilenv.TestServiceDiscAddr, utilenv.GetStunServers()}
		}
	}
	conf := new(V1)
	if common != nil {
		conf.Common = common
	}
	conf.Dmsg = &dmsgc.DmsgConfig{
		Discovery:     services.DmsgDiscovery, //utilenv.DmsgDiscAddr,
		SessionsCount: 1,
		Servers:       []*disc.Entry{},
	}
	conf.Transport = &Transport{
		Discovery:         services.TransportDiscovery, //utilenv.TpDiscAddr,
		AddressResolver:   services.AddressResolver,    //utilenv.AddressResolverAddr,
		PublicAutoconnect: skyenv.PublicAutoconnect,
	}
	conf.Routing = &Routing{
		RouteFinder:        services.RouteFinder, //utilenv.RouteFinderAddr,
		SetupNodes:         services.SetupNodes,  //[]cipher.PubKey{utilenv.MustPK(utilenv.SetupPK)},
		RouteFinderTimeout: DefaultTimeout,
	}
	conf.Launcher = &Launcher{
		ServiceDisc: services.ServiceDiscovery, //utilenv.ServiceDiscAddr,
		Apps:        nil,
		ServerAddr:  skyenv.AppSrvAddr,
		BinPath:     skyenv.AppBinPath,
	}
	conf.UptimeTracker = &UptimeTracker{
		Addr: services.UptimeTracker, //utilenv.UptimeTrackerAddr,
	}
	conf.CLIAddr = skyenv.RPCAddr
	conf.LogLevel = skyenv.LogLevel
	conf.LocalPath = skyenv.LocalPath
	conf.StunServers = services.StunServers //utilenv.GetStunServers()
	conf.ShutdownTimeout = DefaultTimeout
	conf.RestartCheckDelay = Duration(restart.DefaultCheckDelay)

	conf.Dmsgpty = &Dmsgpty{
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DmsgPtyCLINet,
		CLIAddr:  skyenv.DmsgPtyCLIAddr(),
	}

	conf.STCP = &network.STCPConfig{
		ListeningAddress: skyenv.STCPAddr,
		PKTable:          nil,
	}
	// Use dmsg urls for services and add dmsg-servers
	if dmsgHTTP {
		if dmsgHTTPServersList != nil {
			if testEnv {
				conf.Dmsg.Servers = dmsgHTTPServersList.Test.DMSGServers
				conf.Dmsg.Discovery = dmsgHTTPServersList.Test.DMSGDiscovery
				conf.Transport.AddressResolver = dmsgHTTPServersList.Test.AddressResolver
				conf.Transport.Discovery = dmsgHTTPServersList.Test.TransportDiscovery
				conf.UptimeTracker.Addr = dmsgHTTPServersList.Test.UptimeTracker
				conf.Routing.RouteFinder = dmsgHTTPServersList.Test.RouteFinder
				conf.Launcher.ServiceDisc = dmsgHTTPServersList.Test.ServiceDiscovery
			} else {
				conf.Dmsg.Servers = dmsgHTTPServersList.Prod.DMSGServers
				conf.Dmsg.Discovery = dmsgHTTPServersList.Prod.DMSGDiscovery
				conf.Transport.AddressResolver = dmsgHTTPServersList.Prod.AddressResolver
				conf.Transport.Discovery = dmsgHTTPServersList.Prod.TransportDiscovery
				conf.UptimeTracker.Addr = dmsgHTTPServersList.Prod.UptimeTracker
				conf.Routing.RouteFinder = dmsgHTTPServersList.Prod.RouteFinder
				conf.Launcher.ServiceDisc = dmsgHTTPServersList.Prod.ServiceDiscovery
			}
		}
	}
	conf.IsPublic = skyenv.IsPublic
	return conf
}

// MakeDefaultConfig returns the default visor config from a given secret key (if specified).
// The config's 'sk' field will be nil if not specified.
// Generated config will be saved to 'confPath'.
// This function always returns the latest config version.
func MakeDefaultConfig(log *logging.MasterLogger, sk *cipher.SecKey, usrEnv bool, pkgEnv bool, testEnv bool, dmsgHTTP bool, hypervisor bool, confPath, hypervisorPKs string, services *Services) (*V1, error) {
	if usrEnv && pkgEnv {
		log.Fatal("usrEnv and pkgEnv are mutually exclusive")
	}
	cc, err := NewCommon(log, confPath, sk)
	if err != nil {
		return nil, err
	}
	var dmsgHTTPServersList *DmsgHTTPServers

	if dmsgHTTP {
		dmsgHTTPPath := skyenv.DMSGHTTPName
		if pkgEnv {
			dmsgHTTPPath = skyenv.SkywirePath + "/" + skyenv.DMSGHTTPName
		}
		serversListJSON, err := os.ReadFile(filepath.Clean(dmsgHTTPPath))
		if err != nil {
			log.WithError(err).Fatal("Failed to read dmsghttp-config.json file.")
		}
		err = json.Unmarshal(serversListJSON, &dmsgHTTPServersList)
		if err != nil {
			log.WithError(err).Fatal("Error during parsing servers list")
		}
	}
	// Actual config generation.
	conf := MakeBaseConfig(cc, testEnv, dmsgHTTP, services, dmsgHTTPServersList)

	conf.Launcher.Apps = makeDefaultLauncherAppsConfig()

	conf.Hypervisors = make([]cipher.PubKey, 0)

	// Manipulate Hypervisor PKs
	if hypervisorPKs != "" {
		keys := strings.Split(hypervisorPKs, ",")
		for _, key := range keys {
			if key != "" {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					log.WithError(err).Fatalf("Failed to parse hypervisor public key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))

				// Compare key value and visor PK, if same, then this visor should be hypervisor
				if key == conf.PK.Hex() {
					hypervisor = true
					conf.Hypervisors = []cipher.PubKey{}
					break
				}
			}
		}
	}
	if hypervisor {
		config := hypervisorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}
	if pkgEnv {
		pkgconfig := skyenv.PackageConfig()
		conf.LocalPath = pkgconfig.LocalPath
		conf.Launcher.BinPath = pkgconfig.Launcher.BinPath
		if conf.Hypervisor != nil {
			conf.Hypervisor.EnableAuth = pkgconfig.Hypervisor.EnableAuth
			conf.Hypervisor.DBPath = pkgconfig.Hypervisor.DbPath
		}
	}
	if usrEnv {
		usrconfig := skyenv.UserConfig()
		conf.LocalPath = usrconfig.LocalPath
		conf.Launcher.BinPath = usrconfig.Launcher.BinPath
		if conf.Hypervisor != nil {
			conf.Hypervisor.EnableAuth = usrconfig.Hypervisor.EnableAuth
			conf.Hypervisor.DBPath = usrconfig.Hypervisor.DbPath
		}
	}
	return conf, nil
}

// SetDefaultTestingValues mutates configuration to use testing values
// makeDefaultLauncherAppsConfig creates default launcher config for apps,
// for package based installation in other platform (Darwin, Windows) it only includes
// the shipped apps for that platforms
func makeDefaultLauncherAppsConfig() []appserver.AppConfig {
	defaultConfig := []appserver.AppConfig{
		{
			Name:      skyenv.VPNClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
		},
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
	}
	return defaultConfig
}

// DmsgHTTPServers struct use to unmarshal dmsghttp file
type DmsgHTTPServers struct {
	Test DmsgHTTPServersData `json:"test"`
	Prod DmsgHTTPServersData `json:"prod"`
}

// DmsgHTTPServersData is a part of DmsgHTTPServers
type DmsgHTTPServersData struct {
	DMSGServers        []*disc.Entry `json:"dmsg_servers"`
	DMSGDiscovery      string        `json:"dmsg_discovery"`
	TransportDiscovery string        `json:"transport_discovery"`
	AddressResolver    string        `json:"address_resolver"`
	RouteFinder        string        `json:"route_finder"`
	UptimeTracker      string        `json:"uptime_tracker"`
	ServiceDiscovery   string        `json:"service_discovery"`
}
