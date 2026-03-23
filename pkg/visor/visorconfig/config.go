// Package visorconfig pkg/visor/visorconfig/config.go
package visorconfig

import (
	"encoding/json"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsgpty"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// MakeBaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
// This function always returns the latest config version.
func MakeBaseConfig(common *Common, testEnv bool, dmsgHTTP bool, services *Services, dmsgHTTPServersList *DmsgHTTPServers) *V1 {

	//check if any services were passed
	if services == nil {
		var envServices deployment.EnvServices
		if err := json.Unmarshal(deployment.ServicesJSON, &envServices); err != nil {
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
		Protocol:             "yamux",
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
		MinHops:            1,
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
	// Initialize log server config (disabled by default - set local_addr to enable)
	conf.LogServer = &LogServer{
		LocalAddr: "", // Empty = disabled. Set to e.g. "localhost:8002" to enable localhost serving
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
	conf.PublicVisorConfig = &PublicVisorConfig{
		RegistrationTimeout: Duration(PublicVisorRegistrationTimeout),
		MaxTransports:       PublicVisorMaxTransports,
	}
	conf.GeoIP = GeoIP
	return conf
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
