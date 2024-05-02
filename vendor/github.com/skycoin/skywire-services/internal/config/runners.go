package config

import "fmt"

const (
	// DockerRedisCmdTemplate is a template for running dockerized redis
	DockerRedisCmdTemplate string = `
docker container rm redis -f || \
docker run -d  --network=%v \
		--hostname=redis 	\
		--name=redis  redis`

	// DockerDmsgDiscoveryCmdTemplate is a template for running dockerized dmsg-discovery
	DockerDmsgDiscoveryCmdTemplate string = `
docker container rm dmsg-discovery -f || \
docker run -d --network=%v  	\
				--name=dmsg-discovery	\
				--hostname=dmsg-discovery	skywire-services  \
				bash -c "./dmsg-discovery --redis redis://redis:6379"`

	// DockerDmsgServerCmdTemplate is a template for running dockerized dmsg-server
	DockerDmsgServerCmdTemplate string = `
docker container rm dmsg-server -f || \
docker run -d --network=%v  	\
				--name=dmsg-server	\
				--hostname=dmsg-server	skywire-services \
				bash -c "./dmsg-server dmsg-server.json"`

	// DockerTransportDiscoveryCmdTemplate is a template for running dockerized transport-discovery
	DockerTransportDiscoveryCmdTemplate string = `
docker container rm transport-discovery -f || \
docker run -d --network=%v  	\
				--name=transport-discovery	\
				--hostname=transport-discovery	skywire-services \
					bash -c "./transport-discovery"`

	// DockerRouteFinderCmdTemplate is a template for running dockerized route-finder
	DockerRouteFinderCmdTemplate string = `
docker container rm route-finder -f || \
docker run -d --network=%v \
				--name=route-finder	\
				--hostname=route-finder	skywire-services  \
				bash -c "./route-finder --redis redis://redis:6379"`

	// DockerAddressResolverCmdTemplate is a template for running dockerized address-resolver
	DockerAddressResolverCmdTemplate string = `
docker container rm address-resolver -f || \
docker run -d --network=%v\
				--name=address-resolver	\
				--hostname=address-resolver	skywire-services \
				bash -c "./address-resolver address-resolver.json"`

	// DockerSetupNodeCmdTemplate is a template for running dockerized setup-node
	DockerSetupNodeCmdTemplate string = `
docker container rm setup-node -f || \
docker run -d --network=%v\
					--name=setup-node	\
					--hostname=setup-node	skywire-services \
					bash -c "./setup-node setup-node.json"`

	// DockerSkywireVisorCmdTemplate is a template for running dockerized skywire-visor
	DockerSkywireVisorCmdTemplate string = `
docker run -it -v $(shell pwd)/node:/sky --network=%v \
	--name={{.Name}} skywire-runner bash -c "cd /sky && ./skywire-visor {{.Name}}.json"`
)

// DefaultDockerRunners returns set of default dockerized runners
func DefaultDockerRunners(network string) RunnersConfig {
	return RunnersConfig{
		DmsgDiscovery:      fmt.Sprintf(DockerDmsgDiscoveryCmdTemplate, network),
		DmsgServer:         fmt.Sprintf(DockerDmsgServerCmdTemplate, network),
		TransportDiscovery: fmt.Sprintf(DockerTransportDiscoveryCmdTemplate, network),
		RouteFinder:        fmt.Sprintf(DockerDmsgServerCmdTemplate, network),
		SetupNode:          fmt.Sprintf(DockerSetupNodeCmdTemplate, network),
		SkywireVisor:       fmt.Sprintf(DockerSkywireVisorCmdTemplate, network),
		AddressResolver:    fmt.Sprintf(DockerAddressResolverCmdTemplate, network),
	}

}

// DefaultLocalRunners returns set of default runners on localhost
func DefaultLocalRunners() RunnersConfig {
	return RunnersConfig{
		DmsgDiscovery:      "dmsg-discovery --tag {{.Name}}",
		DmsgServer:         "dmsg-server {{.Name}}.json --tag {{.Name}}",
		TransportDiscovery: "transport-discovery {{.Name}}.json --tag {{.Name}}",
		RouteFinder:        "route-finder --tag {{.Name}}",
		SetupNode:          "setup-node {{.Name}}.json --tag {{.Name}}",
		SkywireVisor:       "skywire-visor {{.Name}}.json --tag {{.Name}}",
	}
}

// DefaultSourceRunners returns set of default runners on localhost from source
func DefaultSourceRunners() RunnersConfig {
	return RunnersConfig{
		DmsgDiscovery:      "go run ./cmd/dmsg-discovery --tag {{.Name}}",
		DmsgServer:         "go run ./cmd/dmsg-server {{.Name}}.json --tag {{.Name}}",
		TransportDiscovery: "go run ./cmd/transport-discovery {{.Name}}.json --tag {{.Name}}",
		RouteFinder:        "go run ./cmd/route-finder --tag {{.Name}}",
		SetupNode:          "go run ./cmd/setup-node {{.Name}}.json --tag {{.Name}}",
		SkywireVisor:       "go run ./cmd/skywire-visor {{.Name}}.json --tag {{.Name}}",
	}
}
