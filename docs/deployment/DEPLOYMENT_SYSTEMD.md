# Skywire Systemd Deployment

This document describes deploying skywire services directly on the host using systemd.

For Docker Compose deployment, see [DOCKER_DEPLOYMENT.md](DOCKER_DEPLOYMENT.md).

(original documentation at: https://github.com/skycoin/skywire-deployment)

## Skywire Source Code

The following repositories are used for skywire:

* [skywire](https://github.com/skycoin/skywire)
* [dmsg](https://github.com/skycoin/dmsg)
* [skycoin](https://github.com/skycoin/skycoin) - _Note: skycoin is not a critical dependency and is only used with the reward system_

The following repositories have been integrated with the skywire source code and are no longer maintained as separate repositories / source code:

* ~[skywire-utilities](https://github.com/skycoin/skywire-utilities)~
* ~[skywire-services](https://github.com/skycoin/skywire-services)~
* ~[skycoin-service-discovery](https://github.com/skycoin/skycoin-service-discovery)~

All previously separately compiled binaries / commands are now included as subcommands of the main skywire binary compilation from the repository root:

```
$ go run .
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘
(devel)
built with go1.25.6 X:nodwarf5

Usage:
  skywire

Available Commands:
  visor     Skywire Visor
  cli       Command Line Interface for skywire
  svc       Skywire services
  dmsg      DMSG services & utilities
  app       skywire native applications
  skycoin   skycoin daemon & cli

Flags:
  -b, --bv        print runtime/debug.BuildInfo.Main.Version
  -d, --info      print runtime/debug.BuildInfo
  -v, --version   version for skywire

```

```
$ go run . svc
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤ ───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘
	Skywire services

Usage:
  skywire svc

Available Commands:
  tpd       Transport Discovery Server for skywire
  tps       Transport setup server for skywire
  ar        Address Resolver Server for skywire
  rf        Route Finder Server for skywire
  confbs    Config Bootstrap Server for skywire
  conf      print services-config.json file
  se        skywire environment generator
  ut        Uptime Tracker Server for skywire
  sd        Service discovery server
  sn        Route Setup Node for skywire
  nm        Network monitor for skywire VPN and Visor.
  ip        GeoIP service for skywire
  stun      STUN server for skywire
```

```
$ go run . dmsg
┌┬┐┌┬┐┌─┐┌─┐
 │││││└─┐│ ┬
─┴┘┴ ┴└─┘└─┘
(devel)
built with go1.25.6 X:nodwarf5

Usage:
  skywire dmsg

Available Commands:
  conf      dmsg deployment servers config
  curl      DMSG curl utility
  disc      DMSG Discovery Server
  http      DMSG http file server
  ip        DMSG IP utility
  pty       DMSG pseudoterminal (pty)
  server    DMSG Server
  socks     DMSG socks5 proxy server & client
  web       DMSG resolving proxy & browser client

Flags:
  -b, --bv     print runtime/debug.BuildInfo.Main.Version
  -d, --info   print runtime/debug.BuildInfo
```

## Table of Contents
* [Skywire Deployment Documentation](#skywire-deployment-documentation)
   * [Skywire Source Code](skywire-source-code)
   * [Table of Contents](#table-of-contents)
   * [Code Formatting](#code-formatting)
   * [Code Linting](#code-linting)
   * [Code Tests](#code-tests)
   * [Building](#building)
   * [Runtime Dependencies by Service](#runtime-dependencies-by-service)
      * [Redis](#redis)
         * [Redis setup](#redis-setup)
      * [Postgres](#postgres)
         * [Postgres DB Setup](#postgres-db-setup)
   * [Required Services](#required-services)
   * [Key generation for services](#key-generation-for-services)
      * [Visor Config Bootstrap Endpoint](#visor-config-bootstrap-endpoint)
   * [SKYCOIN-SERVICE-DISCOVERY Setup](#skycoin-service-discovery-setup)
   * [service-discovery](#service-discovery)
   * [DMSG Setup](#dmsg-setup)
      * [dmsg-discovery](#dmsg-discovery)
      * [dmsg-server](#dmsg-server)
         * [Configure dmsg-server](#configure-dmsg-server)
         * [Run dmsg-server](#run-dmsg-server)
   * [SKYWIRE-SERVICES Setup](#skywire-services-setup)
      * [address-resolver](#address-resolver)
      * [route-finder](#route-finder)
      * [transport-discovery](#transport-discovery)
   * [SKYWIRE Setup](#skywire-setup)
      * [Route setup-node](#route-setup-node)
      * [skywire-visor](#skywire-visor)
   * [Using Dmsg to connect to the deployment](#using-dmsg-to-connect-to-the-deployment)
   * [HTTP Service Configuration](#http-service-configuration)


## Code Formatting

Dependencies:
* [go](https://go.dev/)
* [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports)
* [goimports-reviser](https://github.com/incu6us/goimports-reviser)

`make format` - when run from the repository root (i.e. `../../../../github.com/skycoin/skywire`) executes a variation of the following commands, depending on the repo ([skywire](https://github.com/skycoin/skywire) or [dmsg](https://github.com/skycoin/dmsg)):
```
go mod tidy -v
[[ -d pkg ]] && goimports -w -local github.com/skycoin/$(basename $(pwd)) ./pkg
[[ -d cmd ]] && goimports -w -local github.com/skycoin/$(basename $(pwd)) ./cmd
[[ -d internal ]] && goimports -w -local github.com/skycoin/$(basename $(pwd)) ./internal
find . -type f -name '*.go' -not -path "./.git/*" -not -path "./vendor/*"  -exec goimports-reviser -project-name github.com/skycoin/$(basename $(pwd)) {} \;
```

## Code Linting

Dependencies:
* [go](https://go.dev/)
* [golangci-lint](https://golangci-lint.run/)

`make lint` run from the repository root executes a variation of the following commands, depending on the repo ([skywire](https://github.com/skycoin/skywire) or [dmsg](https://github.com/skycoin/dmsg)):
```
golangci-lint run -c .golangci.yml ./...
```

## Code Tests

Dependencies:
* [go](https://go.dev/)

_Note: `make check` combines the `lint` and `test` directives and is currently used by the CI to check pull requests._

`make test` run from the repository root executes a variation of the following commands, depending on the repo ([skywire](https://github.com/skycoin/skywire) or [dmsg](https://github.com/skycoin/dmsg)):
```
go clean -testcache &>/dev/null
[[ -d internal ]] && go test  -cover -timeout=5m -mod=vendor ./internal/...
[[ -d pkg ]] && go test -cover -timeout=5m -mod=vendor ./pkg/...
```

_Note: The flags used for `go test` may vary from repo to repo_

## Building

Dependencies:
* [go](https://go.dev/)


`make build` run from the repository root executes `go build` for ~all the binaries that can be built from .go files in `./cmd` subdirectories,~ the default compilation from the repository root (i.e. `../../../../github.com/skycoin/skywire`), which includes all binaries that might otherwise be separately compiled as subcommands, for example:

```
go build .
```

It is also possible to `go run` without explicitly compiling a binary

```
go run . --help
```

Note that some binaries may have gcc / libc6 dependency unless statically compiled with musl.

# Unified binary

The releases for skywire now comprise a single binary executable file with subcommands for every previously separately-compiled binary. It is recommended, for the sake of simpliciity, to compile only this one binary from the skywire repo.

**This document will henceforth refer to the unified binary compilation and assume that it exists in the executable `PATH`.**

The Makefile directives in skywire have been updated (on develop branch) to only produce the new unified binary; separate compilations are still possible but should be considered deprecated.

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/466c9b42-c55f-4614-a4e6-3187fab49a8e)

combined documentation can be found [here](https://github.com/skycoin/skywire/tree/develop/cmd/skywire)

## Runtime Dependencies by Service

### Redis
* [address-resolver](#address-resolver)
* [transport-discovery](#transport-discovery)
* [dmsg-discovery](#dmsg-discovery)
* [service-discovery](#service-discovery)

#### Redis setup

Redis setup simply entails installing redis and starting the service (which may now be called `valkey.service`).

### Postgres
* ~[transport-discovery](#transport-discovery)~
* ~[route-finder](#route-finder)~
* ~[service-discovery](#service-discovery)~

## Required Services

* [address-resolver](#address-resolver)
* [dmsg-discovery](#dmsg-discovery)
* [dmsg-server](#dmsg-server)
* [transport-discovery](#transport-discovery)
* [route-finder](#route-finder)
* [service-discovery](#service-discovery)
* [setup-node](#route-setup-node)

## Key generation for services

Public/secret keypairs for all services have identical format can can be used interchangeably. Generate a keypair with `skywire cli config gen-keys`:

```
skywire cli config gen-keys
```

The keypair can be written to a file and used by a service which accepts it in the following way

```
skywire cli config gen-keys | tee dmsgd-config.json
skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)
```

### Visor Config Bootstrap Endpoint

The following endpoint is queried by the skywire visor on `skywire cli config gen`:
https://conf.skywire.skycoin.com/

This endpoint provides json which will become part of the visor's config.

When setting up a custom deployment, the following file is created (manually) to reflect your deployment:
```
{
  "dmsg_discovery": "http://dmsgd.skywire.skycoin.com",
  "transport_discovery": "http://tpd.skywire.skycoin.com",
  "address_resolver": "http://ar.skywire.skycoin.com",
  "route_finder": "http://rf.skywire.skycoin.com",
  "route_setup_nodes": [
    "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557",
    "024fbd3997d4260f731b01abcfce60b8967a6d4c6a11d1008812810ea1437ce438",
    "03b87c282f6e9f70d97aeea90b07cf09864a235ef718725632d067873431dd1015"
  ],
  "transport_setup": [
    "03530b786c670fc7f5ab9021478c7ec9cd06a03f3ea1416c50c4a8889ef5bba80e",
    "03271c0de223b80400d9bd4b7722b536a245eb6c9c3176781ee41e7bac8f9bad21",
    "03a792e6d960c88c6fb2184ee4f16714c58b55f0746840617a19f7dd6e021699d9",
    "0313efedc579f57f05d4f5bc3fbf0261f31e51cdcfde7e568169acf92c78868926",
    "025c7bbf23e3441a36d7e8a1e9d717921e2a49a2ce035680fec4808a048d244c8a",
    "030eb6967f6e23e81db0d214f925fc5ce3371e1b059fb8379ae3eb1edfc95e0b46",
    "02e582c0a5e5563aad47f561b272e4c3a9f7ac716258b58e58eb50afd83c286a7f",
    "02ddc6c749d6ed067bb68df19c9bcb1a58b7587464043b1707398ffa26a9746b26",
    "03aa0b1c4e23616872058c11c6efba777c130a85eaf909945d697399a1eb08426d",
    "03adb2c924987d8deef04d02bd95236c5ae172fe5dfe7273e0461d96bf4bc220be"
  ],
  "uptime_tracker": "http://ut.skywire.skycoin.com",
  "service_discovery": "http://sd.skycoin.com",
  "stun_servers": [
    "192.53.117.238:3478",
    "170.187.228.44:3478",
    "192.53.117.237:3478",
    "192.53.117.146:3478",
    "192.53.117.60:3478",
    "192.53.117.124:3478",
    "170.187.228.178:3478",
    "170.187.225.246:3478"
  ],
  "dns_server": "1.1.1.1",
  "survey_whitelist": [
    "02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637",
    "03714c8bdaee0fb48f47babbc47c33e1880752b6620317c9d56b30f3b0ff58a9c3",
    "020d35bbaf0a5abc8ec0ba33cde219fde734c63e7202098e1f9a6cf9daaeee55a9",
    "027f7dec979482f418f01dfabddbd750ad036c579a16422125dd9a313eaa59c8e1",
    "031d4cf1b7ab4c789b56c769f2888e4a61c778dfa5fe7e5cd0217fc41660b2eb65",
    "0327e2cf1d2e516ecbfdbd616a87489cc92a73af97335d5c8c29eafb5d8882264a",
    "03abbb3eff140cf3dce468b3fa5a28c80fa02c6703d7b952be6faaf2050990ebf4"
  ]
}

```

This endpoint is used to avoid relying on hardcoded defaults for the production deployment endpoints in the visor's config. This config is still hardcoded for fallback scenarios.
Without a config bootstrapper service / endpoint, any changes to the deployment would require an updated version of skywire to manifest correct config default values ; or another mechanism for distributing the deployment configuration.

A deployment of _all services as a set_ will use this for visors to query the endpoint during `config gen` to get the services for the custom deployment, otherwise it is not strictly required.

~Note as of v1.3.19 the services as defined in the conf service are distributed with the skywire binary releases as `services-config.json`~

Note: deployment defaults are now embedded, but services-config.json and dmsghttp-config.json may be specified to `config gen` to override the hardcoded deployment defaults, to support a custom deployment.

## SKYCOIN-SERVICE-DISCOVERY Setup

This section deals with skycoin-service-discovery or `skywire svc sd`

## `service-discovery`
[service-discovery](https://github.com/skycoin/skycoin-service-discovery/tree/develop/cmd/service-discovery)

_Note: this service requires redis_

Run the Service Discovery server
```
skywire cli config gen-keys > sd-config.json
skywire svc sd  --addr ":9098" --redis "redis://localhost:6379" --dmsg-disc "http://127.0.0.1:9090" --sk $(tail -n1 sd-config.json)
```
![image](https://github.com/skycoin/skywire-deployment/assets/36607567/fa549212-63a9-49bb-a321-74f19560074f)


## DMSG Setup

This section deals with dmsg deployment setup.

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/1e8eede0-2ebf-4ec2-bc34-c3314136f52d)

### `dmsg-discovery`
[dmsg-discovery](https://github.com/skycoin/dmsg/tree/develop/cmd/dmsg-discovery)

_Note: this service requires redis_

Run the Dmsg Discovery server
```
skywire cli config gen-keys > dmsgd-config.json
skywire dmsg disc --addr ":9090" --redis "redis://localhost:6379" --sk $(tail -n1 dmsgd-config.json)
```
![image](https://github.com/skycoin/skywire-deployment/assets/36607567/03c6788b-cd36-4f80-bba2-da179f09f78c)

### `dmsg-server`
[dmsg-server](https://github.com/skycoin/dmsg/tree/develop/cmd/dmsg-server)

#### Configure `dmsg-server`
Generate a config for the dmsg server
```
skywire dmsg server config gen -o dmsg-config.json
```
output
```
{
	"public_key": "02a4072022716ee7fb8205edd4286dc73abe514ff8a7a74c6a27131c6efbe71876",
	"secret_key": "62bcd18c5693ad1d2a24239f7303a84d6afb6966340fc70afe93579987cceb90",
	"discovery": "http://dmsgd.skywire.skycoin.com",
	"public_address": "127.0.0.1:8081",
	"local_address": ":8081",
	"health_endpoint_address": ":8082",
	"log_level": "info",
	"update_interval": 0,
	"max_sessions": 2048
}
```
__The IP address must be a public ip address.__

__The port on which the dmsg server is running must be forwarded or otherwise accessible for public servers.__

The above "discovery" endpoint should be changed to match the endpoint of the dmsg discovery server referenced in the previous section.
The default ports are shown.

#### Run `dmsg-server`

Run the dmsg server
```
skywire dmsg server start dmsg-config.json
```

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/784d03dc-6bc5-48b9-b267-d8f9bc35bdac)

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/2aaa9abe-4235-4ed5-bc74-525067c1aac4)



## SKYWIRE-SERVICES Setup

This section deals with skywire services

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/8c4c1487-bbd4-453a-a6bb-48933f9f2a71)

### `address-resolver`
[address-resolver](https://github.com/skycoin/skywire-services/tree/develop/cmd/address-resolver)
_Note: the specified port must be on a public ip address or port forwarded for udp__
_Note: this service requires redis_

Run the address resolver
```
skywire cli config gen-keys > ar-config.json
skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --dmsg-disc "http://127.0.0.1:9090" --sk $(tail -n1 ar-config.json)
```

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/656ff446-e35a-4854-a81b-e273da6cd561)


### `route-finder`
[route-finder](https://github.com/skycoin/skywire-services/tree/develop/cmd/route-finder)


Run the Route Finder
```
skywire cli config gen-keys >  rf-config.json
skywire svc rf  --addr ":9092" --dmsg-disc "http://127.0.0.1:9090" --sk $(tail -n1 rf-config.json)
```

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/7796136d-9749-49a9-b61b-6606e99e009e)

### `transport-discovery`
[transport-discovery](https://github.com/skycoin/skywire-services/tree/develop/cmd/transport-discovery)

_Note: this service requires redis_


Run the Transport Discovery server
```
skywire cli config gen-keys | tee tpd-config.json
skywire svc tpd  --addr ":9091" --redis "redis://localhost:6379" --dmsg-disc "http://127.0.0.1:9090" --sk $(tail -n1 tpd-config.json)
```

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/69db3d4d-f05b-4415-aa0f-ddddbc8cc856)


## SKYWIRE Setup

### Route `setup-node`
[setup-node](https://github.com/skycoin/skywire/tree/develop/cmd/setup-node)

Running route setup-node requires a config as follows:
```
{
	"public_key": "02a4072022716ee7fb8205edd4286dc73abe514ff8a7a74c6a27131c6efbe71876",
	"secret_key": "62bcd18c5693ad1d2a24239f7303a84d6afb6966340fc70afe93579987cceb90",
	"dmsg": {
		"discovery": "http://dmsgd.skywire.skycoin.com",
		"sessions_count": 1,
		"servers": []
	},
	"transport_discovery": "http://tpd.skywire.skycoin.com",
	"log_level": "debug"
}
```
This configuration can be generated with `skywire cli config gen --sn` and should be repopulated with the endpoints for your deployment.

Run the route setup-node
```
skywire svc sn setup-node-config.json
```

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/9d45d410-be5e-431f-89eb-09a7bddb6600)


### `skywire-visor`
[skywire visor](https://github.com/skycoin/skywire/tree/develop/cmd/skywire-visor)
[skywire cli](https://github.com/skycoin/skywire/tree/develop/cmd/skywire-cli)
[skywire](https://github.com/skycoin/skywire/tree/develop/cmd/skywire)

A brief overview of the visor's use with a new deployment is as follows

Generate a config with defaults for your deployment:

_Note the `-p` flag is used for the linux package installation path_
```
skywire cli config gen -bpirxa conf.haltingstate.net -o skywire-config.json
```
example output
```
{
	"version": "v1.3.20",
	"sk": "30706958e03ef393418df2ed5a24da477730a0101da9b17959d4f1f231624877",
	"pk": "03adb8df92b82320ee1507a131c8cde0a0fdf267b4e43ec1be1849e4f9dbc258c4",
	"dmsg": {
		"discovery": "http://dmsgd.haltingstate.net",
		"sessions_count": 2,
		"servers": [],
		"servers_type": "all"
	},
	"dmsgpty": {
		"dmsg_port": 22,
		"cli_network": "unix",
		"cli_address": "/tmp/dmsgpty.sock",
		"whitelist": []
	},
	"skywire-tcp": {
		"pk_table": null,
		"listening_address": ":7777"
	},
	"transport": {
		"discovery": "http://tpd.haltingstate.net",
		"address_resolver": "http://ar.haltingstate.net",
		"public_autoconnect": true,
		"transport_setup": [
			"03530b786c670fc7f5ab9021478c7ec9cd06a03f3ea1416c50c4a8889ef5bba80e",
			"03271c0de223b80400d9bd4b7722b536a245eb6c9c3176781ee41e7bac8f9bad21",
			"03a792e6d960c88c6fb2184ee4f16714c58b55f0746840617a19f7dd6e021699d9",
			"0313efedc579f57f05d4f5bc3fbf0261f31e51cdcfde7e568169acf92c78868926",
			"025c7bbf23e3441a36d7e8a1e9d717921e2a49a2ce035680fec4808a048d244c8a",
			"030eb6967f6e23e81db0d214f925fc5ce3371e1b059fb8379ae3eb1edfc95e0b46",
			"02e582c0a5e5563aad47f561b272e4c3a9f7ac716258b58e58eb50afd83c286a7f",
			"02ddc6c749d6ed067bb68df19c9bcb1a58b7587464043b1707398ffa26a9746b26",
			"03aa0b1c4e23616872058c11c6efba777c130a85eaf909945d697399a1eb08426d",
			"03adb2c924987d8deef04d02bd95236c5ae172fe5dfe7273e0461d96bf4bc220be"
		],
		"log_store": {
			"type": "file",
			"location": "/opt/skywire/local/transport_logs",
			"rotation_interval": "168h0m0s"
		},
		"stcpr_port": 0,
		"sudph_port": 0
	},
	"routing": {
		"route_setup_nodes": [
			"0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557",
			"024fbd3997d4260f731b01abcfce60b8967a6d4c6a11d1008812810ea1437ce438",
			"03b87c282f6e9f70d97aeea90b07cf09864a235ef718725632d067873431dd1015"
		],
		"route_finder": "http://rf.haltingstate.net",
		"route_finder_timeout": "10s",
		"min_hops": 0
	},
	"uptime_tracker": {
		"addr": "http://ut.skywire.skycoin.com"
	},
	"launcher": {
		"service_discovery": "http://sd.haltingstate.net",
		"apps": [
			{
				"name": "vpn-client",
				"binary": "skywire",
				"args": [
					"app",
					"vpn-client",
					"--dns",
					"1.1.1.1"
				],
				"auto_start": false,
				"port": 43
			},
			{
				"name": "skychat",
				"binary": "skywire",
				"args": [
					"app",
					"skychat",
					"--addr",
					":8001"
				],
				"auto_start": true,
				"port": 1
			},
			{
				"name": "skysocks",
				"binary": "skywire",
				"args": [
					"app",
					"skysocks"
				],
				"auto_start": true,
				"port": 3
			},
			{
				"name": "skysocks-client",
				"binary": "skywire",
				"args": [
					"app",
					"skysocks-client",
					"--addr",
					":1080"
				],
				"auto_start": false,
				"port": 13
			},
			{
				"name": "vpn-server",
				"binary": "skywire",
				"args": [
					"app",
					"vpn-server"
				],
				"auto_start": false,
				"port": 44
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "/opt/skywire/bin",
		"display_node_ip": false
	},
	"survey_whitelist": [
		"02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637",
		"03714c8bdaee0fb48f47babbc47c33e1880752b6620317c9d56b30f3b0ff58a9c3",
		"020d35bbaf0a5abc8ec0ba33cde219fde734c63e7202098e1f9a6cf9daaeee55a9",
		"027f7dec979482f418f01dfabddbd750ad036c579a16422125dd9a313eaa59c8e1",
		"031d4cf1b7ab4c789b56c769f2888e4a61c778dfa5fe7e5cd0217fc41660b2eb65",
		"0327e2cf1d2e516ecbfdbd616a87489cc92a73af97335d5c8c29eafb5d8882264a",
		"03abbb3eff140cf3dce468b3fa5a28c80fa02c6703d7b952be6faaf2050990ebf4"
	],
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "",
	"local_path": "/opt/skywire/local",
	"dmsghttp_server_path": "/opt/skywire/local/custom",
	"stun_servers": [
		"192.53.117.238:3478",
		"170.187.228.44:3478",
		"192.53.117.237:3478",
		"192.53.117.146:3478",
		"192.53.117.60:3478",
		"192.53.117.124:3478",
		"170.187.228.178:3478",
		"170.187.225.246:3478"
	],
	"shutdown_timeout": "10s",
	"is_public": false,
	"persistent_transports": null
}
```

__Note the default ports:__
```
stcp	:7777
cli RPC	:3435
appsrv	:5505
HVUI	:8000
skychat	:8001
```
ports which are by default 0 are selected at random
```
		"stcpr_port": 0,
		"sudph_port": 0
```
__Note: tcp, udp, and http ports in the config will always have a colon and will never be low ports by default!__


The other ports are `virtual` dmsg ports

Run skywire-visor
```
skywire visor -c skywire-config.json -sl debug
```
OR
```
skywire visor -sl debug
```

![image](https://github.com/skycoin/skywire-deployment/assets/36607567/438a0829-6205-4e17-b0d1-e7dfe021ce2d)


![image](https://github.com/skycoin/skywire-deployment/assets/36607567/abf16ece-48f3-4359-ac82-dbfcce810927)

## Using Dmsg to connect to the deployment

A JSON config file called `dmsghttp-config.json` can be created containing the dmsg addresses of the services for a deployment. This configuration is used automatically based on region (iran, china) with `skywire cli config gen -b` or used by default with `skywire cli config gen -d` and must be present at the expected path - either the current directory or the installation directory - for that configuration to be generated. This file is included in the skywire github repository as well as with our releases.

the following file is created manually to reflect the deployment
```
{
  "test": {
    "dmsg_servers": [
      {
        "static":"024716428e6315d954356e9ad72bea32bb2b41aab5a54a9b5cb4313964016e64d8",
        "server":{
          "address":"172.104.188.39:30080"
        }
      },
      {
        "static":"03f6b0a20be8fe0fd2fd0bd850507cfb10d0eaa37dce5c174654d07db5749a41bf",
        "server":{
          "address":"192.53.115.181:8083"
        }
      },
      {
        "static":"02ae49c901480b49f9c40f6f90fa8e775cf9b00b61ded673a9b0776dfb7cccd374",
        "server":{
          "address":"139.162.45.141:8083"
        }
      }
    ],
    "dmsg_discovery": "dmsg://03cd2336e5de74bdab2bbdb44b06b0c8c713a5ee9029615f5526f8c99a6afe87b8:80",
    "transport_discovery": "dmsg://02703cf828ea11d25b2c8eb0796132ecc7e53b22325b20ce3674ce5cd8693e4fb6:80",
    "address_resolver": "dmsg://030eb7d8cf6eac40c19bbc433de6d6b9bb7a47f2e1d7095c6a01aa676471670ad2:80",
    "route_finder": "dmsg://02ece5b69eaee13ef967b7eb67ca93f1dfddad3a51c9cb1808c4bd0d8d8aa32053:80",
    "uptime_tracker": "dmsg://022c788cca11f208cdfd83ed0c2a8c7b661221736c461adc7c6738a2c1b041c7f8:80",
    "service_discovery": "dmsg://038f751df4af75fb3d51f6693602bfe8289145e633ffdd1e67d686bea595f84d55:80"
  },
  "prod": {
    "dmsg_servers": [
      {
        "static":"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd",
        "server":{
          "address":"dmsg.server02a2d4c3.skywire.skycoin.com:30081"
        }
      },
      {
        "static":"03717576ada5b1744e395c66c2bb11cea73b0e23d0dcd54422139b1a7f12e962c4",
        "server":{
          "address":"dmsg.server03717576.skywire.skycoin.com:30082"
        }
      },
      {
        "static":"0228af3fd99c8d86a882495c8e0202bdd4da78c69e013065d8634286dd4a0ac098",
        "server":{
          "address":"45.118.133.242:30084"
        }
      },
      {
        "static":"03d5b55d1133b26485c664cf8b95cff6746d1e321c34e48c9fed293eff0d6d49e5",
        "server":{
          "address":"dmsg.server03d5b55d.skywire.skycoin.com:30083"
        }
      },
      {
        "static":"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server":{
          "address":"192.53.114.142:30085"
        }
      },
      {
        "static":"02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0",
        "server":{
          "address":"dmsg.server02a4.skywire.skycoin.com:30089"
        }
      }
    ],
    "dmsg_discovery": "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80",
    "transport_discovery": "dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80",
    "address_resolver": "dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80",
    "route_finder": "dmsg://039d89c5eedfda4a28b0c58b0b643eff949f08e4f68c8357278081d26f5a592d74:80",
    "uptime_tracker": "dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80",
    "service_discovery": "dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80"
  }
}
```

## HTTP Service Configuration

The following [caddy-server](https://caddyserver.com/) [`Caddyfile`](https://caddyserver.com/docs/caddyfile/concepts#caddyfile-concepts) configuration reflects the default ports of the http services.

```
(common) {
    header /* {
        -Server
    }
}
dmsgd.haltingstate.net {
reverse_proxy http://127.0.0.1:9090
import common
}
ar.haltingstate.net {
reverse_proxy http://127.0.0.1:9093
import common
}
rf.haltingstate.net {
reverse_proxy http://127.0.0.1:9092
import common
}
tpd.haltingstate.net {
reverse_proxy http://127.0.0.1:9091
import common
}
sd.haltingstate.net {
reverse_proxy http://127.0.0.1:9098
import common
}
conf.haltingstate.net {
header Content-Type	application/json
respond {"dmsg_discovery":"http://dmsgd.haltingstate.net","transport_discovery":"http://tpd.haltingstate.net","address_resolver":"http://ar.haltingstate.net","route_finder":"http://rf.haltingstate.net","setup_nodes":["024fbd3997d4260f731b01abcfce60b8967a6d4c6a11d1008812810ea1437ce438"],"uptime_tracker":"http://ut.skywire.skycoin.com","service_discovery":"http://sd.haltingstate.net","stun_servers":["139.162.12.30:3478","170.187.228.181:3478","172.104.161.184:3478","170.187.231.137:3478","143.42.74.91:3478","170.187.225.78:3478","143.42.78.123:3478","139.162.12.244:3478"],"dns_server":"1.1.1.1"}
import common
}
```
