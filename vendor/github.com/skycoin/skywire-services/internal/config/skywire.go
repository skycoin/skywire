package config

import (
	msg "github.com/skycoin/dmsg/pkg/dmsgserver"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/router"
)

const (
	// PublicDmsgDiscovery is global dmsg-discovery service
	PublicDmsgDiscovery string = "https://dmsg.discovery.skywire.skycoin.com"
	// PublicDmsgServer is global dmsg-server service
	PublicDmsgServer string = "https://dmsg.discovery.skywire.skycoin.com"
	// PublicTransportDiscovery is  global transport-discovery service
	PublicTransportDiscovery string = "https://transport.discovery.skywire.skycoin.com"
	// PublicRouteFinder is global route finder service
	PublicRouteFinder string = "https://routefinder.skywire.skycoin.com/"
	// PublicSetupNode is public key of global setup-node
	PublicSetupNode string = "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	// PublicAddressResolver is  global address-resolver service
	PublicAddressResolver string = "https://address.resolver.skywire.skycoin.com"
)

// SkywireConfig defines configuration of skywire-services
type SkywireConfig struct {
	DmsgDiscovery      DmsgDiscoveryConfig      `json:"dmsg_discovery,omitempty"`
	DmsgServer         DmsgServerConfig         `json:"dmsg_server,omitempty"`
	TransportDiscovery TransportDiscoveryConfig `json:"transport_discovery,omitempty"`
	RouteFinder        RouteFinderConfig        `json:"route_finder,omitempty"`
	SetupNode          SetupNodeConfig          `json:"setup_node,omitempty"`
	AddressResolver    AddressResolverConfig    `json:"address_resolver,omitempty"`
}

// DefaultPublicSkywire constructs default global skywire-services configuration
func DefaultPublicSkywire() SkywireConfig {
	sw := SkywireConfig{}

	sw.DmsgDiscovery = DmsgDiscoveryConfig{
		Address: PublicDmsgDiscovery,
	}
	sw.TransportDiscovery = TransportDiscoveryConfig{
		Address: PublicTransportDiscovery,
	}
	sw.RouteFinder = RouteFinderConfig{
		Address: PublicRouteFinder,
	}
	sw.SetupNode = SetupNodeConfig{
		PubKey: _pk(PublicSetupNode),
	}
	sw.AddressResolver = AddressResolverConfig{
		Address: PublicAddressResolver,
	}
	return sw
}

// DmsgDiscoveryConfig defines configuration of dmsg-discovery
type DmsgDiscoveryConfig struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
}

// DmsgServerConfig defines configuration of dmsg-server
type DmsgServerConfig struct {
	Name    string      `json:"name,omitempty"`
	Address string      `json:"address,omitempty"`
	Config  *msg.Config `json:"config,omitempty"`
	Cmd     string      `json:"cmd,omitempty"`
}

// TransportDiscoveryConfig defines configuration of transport-discovery
type TransportDiscoveryConfig struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
}

// RouteFinderConfig defines configuration of route-finder
type RouteFinderConfig struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
}

// SetupNodeConfig defines configuration of setup-node
type SetupNodeConfig struct {
	Name   string              `json:"name,omitempty"`
	PubKey cipher.PubKey       `json:"pk,omitempty"`
	Config *router.SetupConfig `json:"config,omitempty"`
	Cmd    string              `json:"cmd,omitempty"`
}

// AddressResolverConfig defines configuration of address-resolver
type AddressResolverConfig struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
}

// AddDmsgDiscovery adds dmsg-discovery configuration to environment configuration
func (env *EnvConfig) AddDmsgDiscovery(name string, address string) *EnvConfig {
	env.Skywire.DmsgDiscovery = DmsgDiscoveryConfig{
		Name:    name,
		Address: address,
		Cmd:     _cmd(env.Runners.DmsgDiscovery, struct{ Name, Address string }{name, address}),
		// Cmd:     fmt.Sprintf("%v --address %v --tag %v", env.Runners.DmsgDiscovery, address, name),
	}
	return env
}

// AddDmsgServer adds dmsg-server configuration to environment configuration
func (env *EnvConfig) AddDmsgServer(name string, publicAddress string, localAddress string) *EnvConfig {
	pk, sk := cipher.GenerateKeyPair()

	env.Skywire.DmsgServer = DmsgServerConfig{
		Name: name,
		Config: &msg.Config{
			PubKey:        pk,
			SecKey:        sk,
			Discovery:     env.Skywire.DmsgDiscovery.Address,
			PublicAddress: publicAddress,
			LocalAddress:  localAddress,
			LogLevel:      "info",
		},
		Cmd: _cmd(env.Runners.DmsgServer, struct{ Name string }{name}),
	}
	return env
}

// EmptyDmsgServerConfig return empty configuration for dmsg-server
func EmptyDmsgServerConfig() msg.Config {
	pk, sk := cipher.GenerateKeyPair()
	return msg.Config{
		PubKey:        pk,
		SecKey:        sk,
		Discovery:     PublicDmsgDiscovery,
		PublicAddress: PublicDmsgServer,
		LocalAddress:  "",
		LogLevel:      "info",
	}
}

// AddTransportDiscovery adds transport-discovery configuration to environment configuration
func (env *EnvConfig) AddTransportDiscovery(name string, address string) *EnvConfig {
	env.Skywire.TransportDiscovery = TransportDiscoveryConfig{
		Name: name,
		Cmd:  _cmd(env.Runners.TransportDiscovery, struct{ Name, Address string }{name, address}),
	}
	return env
}

// AddRouteFinder adds route-finder configuration to environment configuration
func (env *EnvConfig) AddRouteFinder(name string, address string) *EnvConfig {
	env.Skywire.RouteFinder = RouteFinderConfig{
		Name: name,
		Cmd:  _cmd(env.Runners.RouteFinder, struct{ Name, Address string }{name, address}),
	}
	return env
}

// AddSetupNode adds setup-node configuration to environment configuration
func (env *EnvConfig) AddSetupNode(name string) *EnvConfig {
	pk, sk := cipher.GenerateKeyPair()
	env.Skywire.SetupNode = SetupNodeConfig{
		PubKey: pk,
		Config: &router.SetupConfig{
			PK: pk,
			SK: sk,
			Dmsg: dmsgc.DmsgConfig{
				Discovery:     env.Skywire.DmsgDiscovery.Address,
				SessionsCount: 1,
			},
			TransportDiscovery: env.Skywire.TransportDiscovery.Address,
			LogLevel:           "info",
		},
		Cmd: _cmd(env.Runners.SetupNode, struct{ Name string }{name}),
	}
	return env
}

// AddAddressResolver adds address-resolver configuration to environment configuration
func (env *EnvConfig) AddAddressResolver(name string, address string) *EnvConfig {
	env.Skywire.AddressResolver = AddressResolverConfig{
		Name: name,
		Cmd:  _cmd(env.Runners.AddressResolver, struct{ Name, Address string }{name, address}),
	}
	return env
}

// EmptySetupNodeConfig return empty configuration for setup-node
func EmptySetupNodeConfig() router.SetupConfig {
	pk, sk := cipher.GenerateKeyPair()

	return router.SetupConfig{
		PK: pk,
		SK: sk,
		Dmsg: dmsgc.DmsgConfig{
			Discovery:     PublicDmsgDiscovery,
			SessionsCount: 1,
		},
		TransportDiscovery: PublicTransportDiscovery,
		LogLevel:           "info",
	}
}

func (sw SkywireConfig) String() string {
	return PrintJSON(sw)
}
