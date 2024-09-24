// Package visorconfig pkg/visor/visorconfig/config.go
package visorconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
	coinCipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// MakeBaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
// This function always returns the latest config version.
func MakeBaseConfig(common *Common, testEnv bool, dmsgHTTP bool, services *Services, dmsgHTTPServersList *DmsgHTTPServers) *V1 {

	//check if any services were passed
	if services == nil {
		var envServices skywire.EnvServices
		if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err != nil {
			return nil
		}
		if !testEnv {
			if err := json.Unmarshal(envServices.Prod, &services); err != nil {
				return nil
			}
		} else {
			if err := json.Unmarshal(envServices.Test, &services); err != nil {
				return nil
			}
		}
	}
	conf := new(V1)
	if common != nil {
		conf.Common = common
	}
	conf.Dmsg = &dmsgc.DmsgConfig{
		Discovery:            services.DmsgDiscovery,
		SessionsCount:        1,
		Servers:              []*disc.Entry{},
		ConnectedServersType: "all",
	}
	conf.Transport = &Transport{
		Discovery:         services.TransportDiscovery,
		AddressResolver:   services.AddressResolver,
		PublicAutoconnect: PublicAutoconnect,
		LogStore: &LogStore{
			Type:             FileLogStore,
			Location:         LocalPath + "/" + TpLogStore,
			RotationInterval: DefaultLogRotationInterval,
		},
		SudphPort: 0,
		StcprPort: 0,
	}
	conf.Routing = &Routing{
		RouteFinder:        services.RouteFinder,
		RouteSetupNodes:    services.RouteSetupNodes,
		RouteFinderTimeout: DefaultTimeout,
	}
	conf.Launcher = &Launcher{
		ServiceDisc:   services.ServiceDiscovery,
		Apps:          nil,
		ServerAddr:    AppSrvAddr,
		BinPath:       AppBinPath,
		DisplayNodeIP: false,
	}
	conf.UptimeTracker = &UptimeTracker{
		Addr: services.UptimeTracker,
	}
	conf.CLIAddr = RPCAddr
	conf.LogLevel = LogLevel
	conf.LocalPath = LocalPath
	conf.DmsgHTTPServerPath = LocalPath + "/" + Custom
	conf.StunServers = services.StunServers
	conf.ShutdownTimeout = DefaultTimeout

	conf.Dmsgpty = &Dmsgpty{
		DmsgPort: DmsgPtyPort,
		CLINet:   DmsgPtyCLINet,
		CLIAddr:  dmsgpty.DefaultCLIAddr(),
	}

	conf.STCP = &network.STCPConfig{
		ListeningAddress: STCPAddr,
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
	conf.IsPublic = IsPublic
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
	dnsServer := ""
	var dmsgHTTPServersList *DmsgHTTPServers
	var envServices skywire.EnvServices
	var svcs skywire.Services
	if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err != nil {
		return nil, nil
	}
	if !testEnv {
		if err := json.Unmarshal(envServices.Prod, &svcs); err != nil {
			dnsServer = svcs.DNSServer
		}
	} else {
		if err := json.Unmarshal(envServices.Test, &svcs); err != nil {
			dnsServer = svcs.DNSServer
		}
	}
	if services != nil {
		if services.DNSServer != "" {
			dnsServer = services.DNSServer
		}
	}

	if dmsgHTTP {
		dmsgHTTPPath := DMSGHTTPName
		if pkgEnv {
			dmsgHTTPPath = SkywirePath + "/" + DMSGHTTPName
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

	conf.Launcher.Apps = makeDefaultLauncherAppsConfig(dnsServer)

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
		config := GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}
	if pkgEnv {
		pkgConfig := PackageConfig()
		conf.LocalPath = pkgConfig.LocalPath
		conf.DmsgHTTPServerPath = pkgConfig.LocalPath + "/" + Custom
		conf.Launcher.BinPath = pkgConfig.LauncherBinPath
		conf.Transport.LogStore.Location = pkgConfig.LocalPath + "/" + TpLogStore
		if conf.Hypervisor != nil {
			conf.Hypervisor.EnableAuth = pkgConfig.Hypervisor.EnableAuth
			conf.Hypervisor.DBPath = pkgConfig.Hypervisor.DbPath
		}
	}
	if usrEnv {
		usrConfig := UserConfig()
		conf.LocalPath = usrConfig.LocalPath
		conf.DmsgHTTPServerPath = usrConfig.LocalPath + "/" + Custom
		conf.Launcher.BinPath = usrConfig.LauncherBinPath
		conf.Transport.LogStore.Location = usrConfig.LocalPath + "/" + TpLogStore
		if conf.Hypervisor != nil {
			conf.Hypervisor.EnableAuth = usrConfig.Hypervisor.EnableAuth
			conf.Hypervisor.DBPath = usrConfig.Hypervisor.DbPath
		}
	}
	return conf, nil
}

// SetDefaultTestingValues mutates configuration to use testing values
// makeDefaultLauncherAppsConfig creates default launcher config for apps,
// for package based installation in other platform (Darwin, Windows) it only includes
// the shipped apps for that platforms
func makeDefaultLauncherAppsConfig(dnsServer string) []appserver.AppConfig {
	defaultConfig := []appserver.AppConfig{
		{
			Name:      VPNClientName,
			Binary:    "skywire",
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
			Args:      []string{"app", "vpn-client", "--dns", dnsServer},
		},
		{
			Name:      SkychatName,
			Binary:    "skywire",
			AutoStart: true,
			Port:      routing.Port(skyenv.SkychatPort),
			Args:      []string{"app", "skychat", "--addr", SkychatAddr},
		},
		{
			Name:      SkysocksName,
			Binary:    "skywire",
			AutoStart: true,
			Port:      routing.Port(skyenv.SkysocksPort),
			Args:      []string{"app", "skysocks"},
		},
		{
			Name:      SkysocksClientName,
			Binary:    "skywire",
			AutoStart: false,
			Port:      routing.Port(skyenv.SkysocksClientPort),
			Args:      []string{"app", "skysocks-client", "--addr", SkysocksClientAddr},
		},
		{
			Name:      VPNServerName,
			Binary:    "skywire",
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNServerPort),
			Args:      []string{"app", "vpn-server"},
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
