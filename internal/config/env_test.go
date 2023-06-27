package config

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// ExampleEnvConfig is an example of explicit declaration of minimal configuration
// with global skywire-services and just one skywire-visor
// and no startup/teardown scripts
func ExampleDefaultPublicSkywire() {
	env := EnvConfig{
		Description:      "Example of minimal environment with global skywire-services and single skywire-visor",
		ExternalServices: ExternalServicesConfig{},
		Runners: RunnersConfig{
			SkywireVisor: "skywire-visor",
		},
		Skywire: DefaultPublicSkywire(),
		Visors: []VisorConfig{
			{
				Name:   "VisorA.json",
				Config: DefaultPublicVisorConfig(),
				Cmd:    "skywire-visor VisorA.json",
			},
		},
		Scripts: EnvScripts{},
	}
	fmt.Println(env.Description)
	fmt.Printf("external services: %v\n", env.ExternalServices)
	fmt.Printf("running skywire-visor with: %v\n", env.Runners.SkywireVisor)
	fmt.Printf("with skywire-services: %v\n", env.Skywire)
	fmt.Printf("with skywire visors: %v\n", env.Visors[0].Cmd)
	fmt.Printf("with scripts: %v\n", env.Scripts)

	// Output: Example of minimal environment with global skywire-services and single skywire-visor
	// external services: {}
	// running skywire-visor with: skywire-visor
	// with skywire-services: {
	// 	"dmsg_discovery": {
	// 		"address": "https://dmsg.discovery.skywire.skycoin.com"
	// 	},
	// 	"dmsg_server": {},
	// 	"transport_discovery": {
	// 		"address": "https://transport.discovery.skywire.skycoin.com"
	// 	},
	// 	"route_finder": {
	// 		"address": "https://routefinder.skywire.skycoin.com/"
	// 	},
	// 	"setup_node": {
	// 		"pk": "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	// 	},
	// 	"address_resolver": {
	// 		"address": "https://address.resolver.skywire.skycoin.com"
	// 	}
	// }
	// with skywire visors: skywire-visor VisorA.json
	// with scripts: {}
}

func ExampleEnvConfig_AddVisorExplicitly() {
	env := &EnvConfig{
		Description:      "Example of minimal environment with global skywire-services and single skywire-visor",
		ExternalServices: ExternalServicesConfig{},
		Runners: RunnersConfig{
			SkywireVisor: "skywire-visor",
		},
		Skywire: DefaultPublicSkywire(),
	}
	env.AddVisorExplicitly(VisorConfig{
		Name:   "VisorA",
		Config: DefaultPublicVisorConfig(),
		Cmd:    "skywire-visor VisorA.json",
	})

	fmt.Printf("Visor: %v\n", env.Visors[0].Cmd)

	// Output: Visor: skywire-visor VisorA.json
}

func ExampleEnvConfig_AddVisor() {
	publicEnv := &EnvConfig{
		Description: "Example of environment configuration with global skywire-services and 3 skywire-visors",
		ExternalServices: ExternalServicesConfig{
			SyslogAddress: "localhost:514",
		},
		Runners: RunnersConfig{
			SkywireVisor: "go run ./cmd/skywire-visor {{.Name}}.json --syslog {{.Syslog}} --tag {{.Name}}",
		},
		Skywire: DefaultPublicSkywire(),
	}

	appsA := []appserver.AppConfig{
		{Name: "skychat", Port: 1, AutoStart: true, Args: []string{"-addr", ":8002"}},
	}
	appsB := make([]appserver.AppConfig, 0)
	appsC := []appserver.AppConfig{
		{Name: "skychat", Port: 1, AutoStart: true, Args: []string{"-addr", ":8003"}},
	}

	env := publicEnv.
		AddVisor("VisorA", appsA, ":3435").
		AddVisor("VisorB", appsB, ":3436").
		AddVisor("VisorC", appsC, ":3437")

	for n, visor := range env.Visors {
		fmt.Printf("%v %v: %v\n", n, visor.Name, visor.Cmd)
	}

	// Output: 0 VisorA: go run ./cmd/skywire-visor VisorA.json --syslog localhost:514 --tag VisorA
	// 1 VisorB: go run ./cmd/skywire-visor VisorB.json --syslog localhost:514 --tag VisorB
	// 2 VisorC: go run ./cmd/skywire-visor VisorC.json --syslog localhost:514 --tag VisorC
}

func ExampleEnvConfig_Skywire() {
	localEnv := &EnvConfig{
		Description: "Example of environment configuration with local skywire-services running from source",
		ExternalServices: ExternalServicesConfig{
			SyslogAddress: "localhost:514",
			RedisAddress:  "localhost:6379",
		},
		Runners: RunnersConfig{
			DmsgDiscovery:      "go run ./cmd/dmsg-discovery --address {{.Address}} --syslog {{.Syslog}} --tag {{.Name}}",
			DmsgServer:         "go run ./cmd/dmsg-server {{.Name}}.json --syslog {{.Syslog}} --tag {{.Name}}",
			TransportDiscovery: "go run ./cmd/transport-discovery --address {{.Address}} --syslog {{.Syslog}} --tag {{.Name}}",
			RouteFinder:        "go run ./cmd/route-finder --address {{.Address}} --syslog {{.Syslog}} --tag {{.Name}}",
			SetupNode:          "go run ./cmd/setup-node {{.Name}}.json --syslog {{.Syslog}} --tag {{.Name}}",
			AddressResolver:    "go run ./cmd/address-resolver {{.Name}}.json --address {{.Address}} --syslog {{.Syslog}} --tag {{.Name}}",
		},
	}

	env := localEnv.
		AddDmsgDiscovery("MSGD", "localhost:12001").
		AddDmsgServer("MSG", "localhost:12002", "localhost:12002").
		AddTransportDiscovery("TRD", "localhost:12003").
		AddRouteFinder("RF", "localhost:12004").
		AddSetupNode("SN").
		AddAddressResolver("AR", "localhost:12005")

	fmt.Println(env.Description)

	fmt.Println(env.Skywire.DmsgDiscovery.Cmd)
	fmt.Println(env.Skywire.DmsgServer.Cmd)
	fmt.Println(env.Skywire.TransportDiscovery.Cmd)
	fmt.Println(env.Skywire.RouteFinder.Cmd)
	fmt.Println(env.Skywire.SetupNode.Cmd)
	fmt.Println(env.Skywire.AddressResolver.Cmd)

	// Output: Example of environment configuration with local skywire-services running from source
	// go run ./cmd/dmsg-discovery --address localhost:12001 --syslog localhost:514 --tag MSGD
	// go run ./cmd/dmsg-server MSG.json --syslog localhost:514 --tag MSG
	// go run ./cmd/transport-discovery --address localhost:12003 --syslog localhost:514 --tag TRD
	// go run ./cmd/route-finder --address localhost:12004 --syslog localhost:514 --tag RF
	// go run ./cmd/setup-node SN.json --syslog localhost:514 --tag SN
	// go run ./cmd/address-resolver AR.json --address localhost:12005 --syslog localhost:514 --tag AR
}

func ExampleDefaultDockerRunners() {

	dockerEnv := &EnvConfig{
		Description: "Example of environment configuration with dockerized skywire",
		ExternalServices: ExternalServicesConfig{
			SyslogAddress: "syslog:514",
			RedisAddress:  "redis:6379",
		},
		Runners: DefaultDockerRunners("SKYNET_001"),
	}

	env := dockerEnv.
		AddDmsgDiscovery("MSGD", "dmsg-discovery:9090").
		AddDmsgServer("MSG", "dmsg-server:8080", "dmsg-server:8080").
		AddTransportDiscovery("TRD", "transport-discovery:9091").
		AddRouteFinder("RF", "route-finder:9092").
		AddSetupNode("SN").
		AddAddressResolver("AR", "address-resolver:9093")

	fmt.Println(env.Description)

	fmt.Println(env.Skywire.DmsgDiscovery.Cmd)
	fmt.Println(env.Skywire.DmsgServer.Cmd)
	fmt.Println(env.Skywire.TransportDiscovery.Cmd)
	fmt.Println(env.Skywire.RouteFinder.Cmd)
	fmt.Println(env.Skywire.AddressResolver.Cmd)
	fmt.Println(env.Skywire.SetupNode.Cmd)

	// Output: Example of environment configuration with dockerized skywire
	//
	// docker container rm dmsg-discovery -f || \
	// docker run -d --network=SKYNET_001  	\
	// 				--name=dmsg-discovery	\
	// 				--hostname=dmsg-discovery	skywire-services  \
	// 				bash -c "./dmsg-discovery --redis redis://redis:6379"
	//
	// docker container rm dmsg-server -f || \
	// docker run -d --network=SKYNET_001  	\
	// 				--name=dmsg-server	\
	// 				--hostname=dmsg-server	skywire-services \
	// 				bash -c "./dmsg-server dmsg-server.json"
	//
	// docker container rm transport-discovery -f || \
	// docker run -d --network=SKYNET_001  	\
	// 				--name=transport-discovery	\
	// 				--hostname=transport-discovery	skywire-services \
	// 					bash -c "./transport-discovery"
	//
	// docker container rm dmsg-server -f || \
	// docker run -d --network=SKYNET_001  	\
	// 				--name=dmsg-server	\
	// 				--hostname=dmsg-server	skywire-services \
	// 				bash -c "./dmsg-server dmsg-server.json"
	//
	// docker container rm address-resolver -f || \
	// docker run -d --network=SKYNET_001\
	// 				--name=address-resolver	\
	// 				--hostname=address-resolver	skywire-services \
	// 				bash -c "./address-resolver address-resolver.json"
	//
	// docker container rm setup-node -f || \
	// docker run -d --network=SKYNET_001\
	// 					--name=setup-node	\
	// 					--hostname=setup-node	skywire-services \
	// 					bash -c "./setup-node setup-node.json"

}
