// Package visorconfig pkg/visor/visorconfig/config.go
package visorconfig

import (
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/skyenv"
	tnspec "github.com/skycoin/skywire/pkg/transport/network/spec"
)

// MakeBaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
// This function always returns the latest config version.
func MakeBaseConfig(common *Common, testEnv bool, dmsgHTTP bool, services *Services, dmsgHTTPServersList *DmsgHTTPServers) *V1 {

	// Pick the deployment-default Services bundle when the caller
	// didn't supply one. Previously this re-unmarshalled
	// deployment.ServicesJSON via encoding/json; the deployment
	// package's init() already does that work and exposes the
	// result as deployment.Prod / deployment.Test, so we just point
	// at those structs. Eliminates two json.Unmarshal calls from
	// the WASM build graph (visorconfig.Services is now a type
	// alias to deployment.Services — same memory shape).
	if services == nil {
		if testEnv {
			services = &deployment.Test
		} else {
			services = &deployment.Prod
		}
	}
	conf := new(V1)
	if common != nil {
		conf.Common = common
	}
	conf.Dmsg = &dmsgspec.DmsgConfig{
		Discovery:            services.DmsgDiscovery,
		SessionsCount:        1,
		Servers:              []*disc.Entry{},
		ConnectedServersType: "all",
		Protocol:             "yamux",
	}
	conf.Transport = &Transport{
		Discovery:         services.TransportDiscovery,
		AddressResolver:   services.AddressResolver,
		PublicAutoconnect: skyenv.PublicAutoconnect,
		LogStore: &LogStore{
			Type:             FileLogStore,
			Location:         skyenv.LocalPath + "/" + skyenv.TpLogStore,
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
		ServerAddr:    skyenv.AppSrvAddr,
		BinPath:       skyenv.AppBinPath,
		DisplayNodeIP: false,
	}
	conf.UptimeTracker = &UptimeTracker{
		Addr: services.UptimeTracker,
	}
	conf.CLIAddr = skyenv.RPCAddr
	conf.LogLevel = skyenv.LogLevel
	conf.LocalPath = skyenv.LocalPath
	conf.StunServers = services.StunServers
	conf.RewardSystem = services.RewardSystem
	conf.RewardSystemDmsg = services.RewardSystemDmsg
	// Supplement from embedded deployment config if conf service didn't provide them
	if conf.RewardSystem == "" {
		conf.RewardSystem = deployment.Prod.RewardSystem
	}
	if conf.RewardSystemDmsg == "" {
		conf.RewardSystemDmsg = deployment.Prod.RewardSystemDmsg
	}
	conf.ShutdownTimeout = DefaultTimeout

	conf.Dmsgpty = &Dmsgpty{
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DmsgPtyCLINet,
		CLIAddr:  defaultDmsgPtyCLIAddr(),
	}

	conf.STCP = &tnspec.STCPConfig{
		ListeningAddress: skyenv.STCPAddr,
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
	conf.IsPublic = skyenv.IsPublic
	conf.PublicVisorConfig = &PublicVisorConfig{
		RegistrationTimeout: Duration(skyenv.PublicVisorRegistrationTimeout),
		MaxTransports:       PublicVisorMaxTransports,
	}
	conf.GeoIP = services.GeoIP
	if conf.GeoIP == "" {
		conf.GeoIP = deployment.Prod.GeoIP
	}
	return conf
}

// defaultDmsgPtyCLIAddr is the conventional unix-style temp-socket
// path the visor's dmsgpty Host listens on. Hardcoded here rather
// than calling pkg/dmsg/pty.DefaultCLIAddr so config.go stays
// WASM-clean (dmsgpty's pty.go pulls in syscall.TIOCGWINSZ + friends
// that don't exist under GOOS=js). Operators on Windows get the
// right path written by cmd/skywire-cli/commands/config/gen.go,
// which still calls pty.DefaultCLIAddr() in its native build.
func defaultDmsgPtyCLIAddr() string {
	return "/tmp/pty.sock"
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
