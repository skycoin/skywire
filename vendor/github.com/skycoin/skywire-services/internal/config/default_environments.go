// Package config internal/config/default_environments.go
package config

import (
	"github.com/skycoin/skywire/pkg/app/appserver"
)

// AddThreeChatVisors add to environment 3 visors:
// VisorA an VisorC running skychat application
// and VisorB to be used as intermediary between them
func (env *EnvConfig) AddThreeChatVisors() *EnvConfig {
	appsA := []appserver.AppConfig{
		{Name: "skychat", Port: 1, AutoStart: true, Args: []string{"-addr", ":8002"}},
	}
	appsB := make([]appserver.AppConfig, 0)
	appsC := []appserver.AppConfig{
		{Name: "skychat", Port: 1, AutoStart: true, Args: []string{"-addr", ":8003"}},
	}

	return env.AddVisor("VisorA", appsA, ":3435").
		AddVisor("VisorB", appsB, ":3436").
		AddVisor("VisorC", appsC, ":3437")
}

// DefaultPublicEnv creates environment with global skywire-services
// and 3 local skywire-visors
func DefaultPublicEnv() *EnvConfig {
	globalEnv := &EnvConfig{
		Description: "Example of environment configuration with global skywire-services and 3 skywire-visors",
		Runners: RunnersConfig{
			SkywireVisor: "skywire-visor {{.Name}}.json",
		},
		Skywire: DefaultPublicSkywire(),
	}

	return globalEnv.AddThreeChatVisors()
}

// DefaultLocalEnv creates environment with local skywire-services
// and 3 local skywire-visors
func DefaultLocalEnv() *EnvConfig {
	localEnv := (&EnvConfig{
		Description: "Example of environment configuration with local skywire-services and 3 chatting skywire-visors",
		Runners:     DefaultLocalRunners(),
	}).AddDmsgDiscovery("MSGD", "http://localhost:12001").
		AddDmsgServer("MSG", "localhost:12002", "localhost:12002").
		AddTransportDiscovery("TRD", "localhost:12003").
		AddRouteFinder("RF", "localhost:12004").
		AddSetupNode("SN").
		AddAddressResolver("AR", "localhost:12005")

	return localEnv.AddThreeChatVisors()
}

// DefaultDockerizedEnv creates environment with dockerized skywire-services
// and 3 local skywire-visors
func DefaultDockerizedEnv(network string) *EnvConfig {
	dockerEnv := (&EnvConfig{
		Description: "Example of environment configuration with local skywire-services and 3 chatting skywire-visors",
		Runners:     DefaultDockerRunners(network),
	}).AddDmsgDiscovery("MSGD", "dmsg-discovery:9090").
		AddDmsgServer("MSG", "dmsg-server:9091", "dmsg-server:9091").
		AddTransportDiscovery("TRD", "http://transport-discovery:9092").
		AddRouteFinder("RF", "route-finder:9093").
		AddSetupNode("SN").
		AddAddressResolver("AR", "http://address-resolver:9093")

	return dockerEnv.AddThreeChatVisors()
}
