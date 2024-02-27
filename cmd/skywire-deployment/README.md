# Skywire Deployment Merged Binary


# skywire documentation

## subcommand tree

A tree representation of the skywire subcommands

```
└─┬skywire
  ├──visor
  ├─┬cli
  │ ├─┬config
  │ │ ├──gen
  │ │ ├──gen-keys
  │ │ ├──check-pk
  │ │ └─┬update
  │ │   ├──dmsghttp
  │ │   ├──svc
  │ │   ├──hv
  │ │   ├──sc
  │ │   ├──ss
  │ │   ├──vpnc
  │ │   └──vpns
  │ ├─┬dmsgpty
  │ │ ├──ui
  │ │ ├──url
  │ │ ├──list
  │ │ └──start
  │ ├─┬visor
  │ │ ├─┬app
  │ │ │ ├──ls
  │ │ │ ├──start
  │ │ │ ├──stop
  │ │ │ ├──register
  │ │ │ ├──deregister
  │ │ │ ├──log
  │ │ │ └─┬arg
  │ │ │   ├──autostart
  │ │ │   ├──killswitch
  │ │ │   ├──secure
  │ │ │   ├──passcode
  │ │ │   └──netifc
  │ │ ├─┬hv
  │ │ │ ├──ui
  │ │ │ ├──cpk
  │ │ │ └──pk
  │ │ ├──pk
  │ │ ├──info
  │ │ ├──ver
  │ │ ├──ports
  │ │ ├──ip
  │ │ ├──ping
  │ │ ├──test
  │ │ ├──start
  │ │ ├──reload
  │ │ ├──halt
  │ │ ├─┬route
  │ │ │ ├──ls-rules
  │ │ │ ├──rule
  │ │ │ ├──rm-rule
  │ │ │ └─┬add-rule
  │ │ │   ├──app
  │ │ │   ├──fwd
  │ │ │   └──intfwd
  │ │ └─┬tp
  │ │   ├──type
  │ │   ├──ls
  │ │   ├──id
  │ │   ├──add
  │ │   ├──rm
  │ │   └──disc
  │ ├─┬vpn
  │ │ ├──start
  │ │ ├──stop
  │ │ ├──status
  │ │ ├──list
  │ │ ├──ui
  │ │ └──url
  │ ├──ut
  │ ├──fwd
  │ ├──rev
  │ ├──reward
  │ ├──rewards
  │ ├──survey
  │ ├──rtfind
  │ ├──rtree
  │ ├─┬mdisc
  │ │ ├──entry
  │ │ └──servers
  │ ├──completion
  │ ├──log
  │ ├─┬proxy
  │ │ ├──start
  │ │ ├──stop
  │ │ ├──status
  │ │ └──list
  │ ├──tree
  │ └──doc
  ├─┬svc
  │ ├──sn
  │ ├──tpd
  │ ├──tps
  │ ├──ar
  │ ├──rf
  │ ├──cb
  │ ├──kg
  │ ├──lc
  │ ├──nv
  │ ├─┬swe
  │ │ ├──visor
  │ │ ├──dmsg
  │ │ └──setup
  │ ├──sd
  │ ├──mn
  │ ├──pvm
  │ ├──ssm
  │ └──vpnm
  ├─┬dmsg
  │ ├─┬pty
  │ │ ├─┬cli
  │ │ │ ├──whitelist
  │ │ │ ├──whitelist-add
  │ │ │ └──whitelist-remove
  │ │ ├─┬host
  │ │ │ └──confgen
  │ │ └──ui
  │ ├──disc
  │ ├─┬server
  │ │ ├─┬config
  │ │ │ └──gen
  │ │ └──start
  │ ├──http
  │ ├──curl
  │ ├─┬web
  │ │ └──gen-keys
  │ ├─┬proxy
  │ │ ├──server
  │ │ └──client
  │ └──mon
  ├─┬app
  │ ├──vpn-server
  │ ├──vpn-client
  │ ├──skysocks-client
  │ ├──skysocks
  │ └──skychat
  ├──tree
  ├──doc
  └──


```

### visor

```

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬  ┬┬┌─┐┌─┐┬─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└┐┌┘│└─┐│ │├┬┘
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └┘ ┴└─┘└─┘┴└─

 Flags:
  -c, --config string    config file to use (default): skywire-config.json
  -C, --confarg string   supply config as argument
  -b, --browser          open hypervisor ui in default web browser
      --systray          run as systray
  -i, --hvui             run as hypervisor [0m*
      --all              show all flags
      --csrf             Request a CSRF token for sensitive hypervisor API requests (default true)


```

### cli

```

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴

Usage:
  skywire cli

Available Commands:
  config                  Generate or update a skywire config
  dmsgpty                 Interact with remote visors
  visor                   Query the Skywire Visor
  vpn                     VPN client
  ut                      query uptime tracker
  fwd                     Control skyforwarding
  rev                     reverse proxy skyfwd
  reward                  skycoin reward address
  rewards                 calculate rewards from uptime data & collected surveys
  survey                  system survey
  rtfind                  Query the Route Finder
  rtree                   map of transports on the skywire network
  mdisc                   Query remote DMSG Discovery
  completion              Generate completion script
  log                     survey & transport log collection
  proxy                   Skysocks client
  tree                    subcommand tree
  doc                     generate markdown docs


```

skywire command line interface

## skywire

```

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

Available Commands:
  visor                   Skywire Visor
  cli                     Command Line Interface for skywire
  svc                     Skywire services
  dmsg                    Dmsg services & utilities
  app                     skywire native applications
  tree                    subcommand tree
  doc                     generate markdown docs


```

## global flags

The skywire-cli interacts with the running visor via rpc calls. By default the rpc server is available on localhost:3435. The rpc address and port the visor is using may be changed in the config file, once generated.

It is not recommended to expose the rpc server on the local network. Exposing the rpc allows unsecured access to the machine over the local network

```

Global Flags:

			--rpc string   RPC server address (default "localhost:3435")

			--json bool   print output as json

```

#### cli config

```
Generate or update the config file used by skywire-visor.

Usage:
  skywire cli config

Available Commands:
  gen                     Generate a config file
  gen-keys                generate public / secret keypair
  check-pk                check a skywire public key
  update                  Update a config file


```

##### cli config gen

```
Generate a config file

	Config defaults file may also be specified with
	SKYENV=/path/to/skywire.conf skywire-cli config gen

Usage:
  skywire cli config gen [flags]

Flags:
  -a, --url string               services conf url

 (default "http://conf.skywire.skycoin.com")
      --loglvl string            level of logging in config[0m (default "info")
  -b, --bestproto                best protocol (dmsg | direct) based on location[0m
  -c, --noauth                   disable authentication for hypervisor UI[0m
  -d, --dmsghttp                 use dmsg connection to skywire services[0m
  -D, --dmsgconf string          dmsghttp-config path[0m (default "dmsghttp-config.json")
      --minsess int              number of dmsg servers to connect to (0 = unlimited)[0m (default 2)
  -e, --auth                     enable auth on hypervisor UI[0m
  -f, --force                    remove pre-existing config[0m
  -g, --disableapps string       comma separated list of apps to disable[0m
  -i, --ishv                     local hypervisor configuration[0m
  -j, --hvpks string             list of public keys to add as hypervisor
      --dmsgpty string           add dmsgpty whitelist PKs
      --survey string            add survey whitelist PKs
      --routesetup string        add route setup node PKs
      --tpsetup string           add transport setup node PKs
  -k, --os string                (linux / mac / win) paths[0m (default "linux")
  -l, --publicip                 allow display node ip in services[0m
  -m, --example-apps             add example apps to the config[0m
  -n, --stdout                   write config to stdout[0m
  -N, --squash                   output config without whitespace or newlines[0m
  -q, --envs                     show the environmental variable settings
  -o, --out string               output config: skywire-config.json[0m
  -p, --pkg                      use path for package: /opt/skywire[0m
  -u, --user                     use paths for user space: /home/d0mo[0m
  -r, --regen                    re-generate existing config & retain keys
  -s, --sk cipher.SecKey         a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --testenv                  use test deployment conf.skywire.dev[0m
  -v, --servevpn                 enable vpn server[0m
  -w, --hide                     dont print the config to the terminal :: show errors with -n flag[0m
  -x, --retainhv                 retain existing hypervisors with regen[0m
  -y, --autoconn                 disable autoconnect to public visors[0m
  -z, --public                   publicize visor in service discovery[0m
      --stcpr int                set tcp transport listening port - 0 for random[0m
      --sudph int                set udp transport listening port - 0 for random[0m
      --binpath string           set bin_path[0m
      --proxyclientpk string     set server public key for proxy client
      --startproxyclient         autostart proxy client
      --noproxyserver            disable autostart of proxy server
      --proxyserverpass string   set proxy server password
      --proxyclientpass string   password for the proxy client to access the server (if needed)
      --killsw string            vpn client killswitch
      --addvpn string            set vpn server public key for vpn client
      --vpnpass string           password for vpn client to access the vpn server (if needed)
      --vpnserverpass string     set password to the vpn server
      --secure string            change secure mode status of vpn server
      --netifc string            VPN Server network interface (detected: eno1)
      --nofetch                  do not fetch the services from the service conf url
  -S, --svcconf string           fallback service configuration file (default "services-config.json")
      --nodefaults               do not use hardcoded defaults for production / test services
      --version string           custom version testing override[0m
      --all                      show all flags


```

##### Example for package / msi

```
$ skywire cli config gen -bpirxn
{
	"version": "v1.3.18",
	"sk": "9d144c6d03595a2a3e8eb5377fe82bf0ec918cce79e0165119662a69d4c73fd6",
	"pk": "02f10962a6e241dd686bae18730b28d5e13c9dd0d9929f6c7712d80e7e515ec295",
	"dmsg": {
		"discovery": "http://dmsgd.skywire.skycoin.com",
		"sessions_count": 2,
		"servers": []
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
		"discovery": "http://tpd.skywire.skycoin.com",
		"address_resolver": "http://ar.skywire.skycoin.com",
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
		"route_finder": "http://rf.skywire.skycoin.com",
		"route_finder_timeout": "10s",
		"min_hops": 0
	},
	"uptime_tracker": {
		"addr": "http://ut.skywire.skycoin.com"
	},
	"launcher": {
		"service_discovery": "http://sd.skycoin.com",
		"apps": [
			{
				"name": "vpn-client",
				"binary": "vpn-client",
				"args": [
					"--dns",
					"1.1.1.1"
				],
				"auto_start": false,
				"port": 43
			},
			{
				"name": "skychat",
				"binary": "skychat",
				"args": [
					"--addr",
					":8001"
				],
				"auto_start": true,
				"port": 1
			},
			{
				"name": "skysocks",
				"binary": "skysocks",
				"auto_start": true,
				"port": 3
			},
			{
				"name": "skysocks-client",
				"binary": "skysocks-client",
				"args": [
					"--addr",
					":1080"
				],
				"auto_start": false,
				"port": 13
			},
			{
				"name": "vpn-server",
				"binary": "vpn-server",
				"auto_start": false,
				"port": 44
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "/opt/skywire/apps",
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
	"persistent_transports": null,
	"hypervisor": {
		"db_path": "/opt/skywire/users.db",
		"enable_auth": true,
		"cookies": {
			"hash_key": "e9535bb8d96b3e116cbc30ed527b0073224e943f1ef9c30a5250132cc782e0e87aa64a97878b625d04359836f37950a21b0feb3abd13e323c4d74202161c955d",
			"block_key": "a16855cc04e3e5c5f1b31c9b5fb9b4d1031a3732553787b8f2d382710187436f",
			"expires_duration": 43200000000000,
			"path": "/",
			"domain": ""
		},
		"dmsg_port": 46,
		"http_addr": ":8000",
		"enable_tls": false,
		"tls_cert_file": "./ssl/cert.pem",
		"tls_key_file": "./ssl/key.pem"
	}
}
```

##### cli config gen-keys

```
generate public / secret keypair

Usage:
  skywire cli config gen-keys


```

##### cli config check-pk

```
check a skywire public key

Usage:
  skywire cli config check-pk <public-key>


```

##### cli config update

```
Update a config file

Usage:
  skywire cli config update [flags]

Available Commands:
  dmsghttp                update dmsghttp-config.json file from config bootstrap service
  svc                     update services-config.json file from config bootstrap service
  hv                      update hypervisor config
  sc                      update skysocks-client config
  ss                      update skysocks-server config
  vpnc                    update vpn-client config
  vpns                    update vpn-server config

Flags:
  -a, --endpoints                update server endpoints
      --log-level string         level of logging in config
  -b, --url string               service config URL: conf.skywire.skycoin.com
  -t, --testenv                  use test deployment: conf.skywire.dev
      --public-autoconn string   change public autoconnect configuration
      --set-minhop int           change min hops value (default -1)
  -i, --input string             path of input config file.
  -o, --output string            config file to output
  -u, --user                     update config at: $HOME/skywire-config.json


```

###### cli config update dmsghttp

```
update dmsghttp-config.json file from config bootstrap service

Usage:
  skywire cli config update dmsghttp [flags]

Flags:
  -p, --path string   path of dmsghttp-config file, default is for pkg installation (default "/opt/skywire/dmsghttp-config.json")

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

###### cli config update svc

```
update services-config.json file from config bootstrap service

Usage:
  skywire cli config update svc [flags]

Flags:
  -p, --path string   path of services-config file, default is for pkg installation (default "/opt/skywire/services-config.json")

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

###### cli config update hv

```
update hypervisor config

Usage:
  skywire cli config update hv [flags]

Flags:
  -+, --add-pks string   public keys of hypervisors that should be added to this visor
  -r, --reset            resets hypervisor configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

###### cli config update sc

```
update skysocks-client config

Usage:
  skywire cli config update sc [flags]

Flags:
  -+, --add-server string   add skysocks server address to skysock-client
  -r, --reset               reset skysocks-client configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

###### cli config update ss

```
update skysocks-server config

Usage:
  skywire cli config update ss [flags]

Flags:
  -s, --passwd string   add passcode to skysocks server
  -r, --reset           reset skysocks configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

###### cli config update vpnc

```
update vpn-client config

Usage:
  skywire cli config update vpnc [flags]

Flags:
  -x, --killsw string       change killswitch status of vpn-client
      --add-server string   add server address to vpn-client
  -s, --pass string         add passcode of server if needed
  -r, --reset               reset vpn-client configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

###### cli config update vpns

```
update vpn-server config

Usage:
  skywire cli config update vpns [flags]

Flags:
  -s, --passwd string      add passcode to vpn-server
      --secure string      change secure mode status of vpn-server
      --autostart string   change autostart of vpn-server
      --netifc string      set default network interface
  -r, --reset              reset vpn-server configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json


```

#### cli dmsgpty

```
Interact with remote visors

Usage:
  skywire cli dmsgpty

Available Commands:
  ui                      Open dmsgpty UI in default browser
  url                     Show dmsgpty UI URL
  list                    List connected visors
  start                   Start dmsgpty session


```

##### cli dmsgpty ui

```
Open dmsgpty UI in default browser

Usage:
  skywire cli dmsgpty ui [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

##### cli dmsgpty url

```
Show dmsgpty UI URL

Usage:
  skywire cli dmsgpty url [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

##### cli dmsgpty list

```
List connected visors

Usage:
  skywire cli dmsgpty list [flags]

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli dmsgpty start

```
Start dmsgpty session

Usage:
  skywire cli dmsgpty start <pk> [flags]

Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")
      --rpc string    RPC server address (default "localhost:3435")


```

#### cli visor

```
Query the Skywire Visor

Usage:
  skywire cli visor [flags]

Available Commands:
  app                     App settings
  hv                      Hypervisor
  pk                      Public key of the visor
  info                    Summary of visor info
  ver                     Version and build info
  ports                   List of Ports
  ip                      IP information of network
  ping                    Ping the visor with given pk
  test                    Test the visor with public visors on network
  start                   start visor
  halt                    Stop a running visor
  route                   View and set rules
  tp                      View and set transports

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor app

```

  App settings

Usage:
  skywire cli visor app [flags]

Available Commands:
  ls                      List apps
  start                   Launch app
  stop                    Halt app
  register                Register app
  deregister              Deregister app
  log                     Logs from app
  arg                     App args

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app ls

```

  List apps

Usage:
  skywire cli visor app ls [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app start

```

  Launch app

Usage:
  skywire cli visor app start <name> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app stop

```

  Halt app

Usage:
  skywire cli visor app stop <name> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app register

```

  Register app

Usage:
  skywire cli visor app register [flags]

Flags:
  -a, --appname string     name of the app
  -p, --localpath string   path of the local folder (default "./local")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app deregister

```

  Deregister app

Usage:
  skywire cli visor app deregister [flags]

Flags:
  -k, --procKey string   proc key of the app to deregister

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app log

```

  Logs from app since RFC3339Nano-formatted timestamp.


  "beginning" is a special timestamp to fetch all the logs

Usage:
  skywire cli visor app log <name> <timestamp> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg

```
App args

Usage:
  skywire cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg autostart

```
App args

Usage:
  skywire cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg killswitch

```
App args

Usage:
  skywire cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg secure

```
App args

Usage:
  skywire cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg passcode

```
App args

Usage:
  skywire cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg netifc

```
App args

Usage:
  skywire cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor hv

```

  Hypervisor


  Access the hypervisor UI

  View remote hypervisor public key

Usage:
  skywire cli visor hv [flags]

Available Commands:
  ui                      open Hypervisor UI in default browser
  cpk                     Public key of remote hypervisor(s) set in config
  pk                      Public key of remote hypervisor(s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor hv ui

```

  open Hypervisor UI in default browser

Usage:
  skywire cli visor hv ui [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor hv cpk

```

  Public key of remote hypervisor(s) set in config

Usage:
  skywire cli visor hv cpk [flags]

Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from /opt/skywire/skywire.json

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor hv pk

```
Public key of remote hypervisor(s) which are currently connected to

Usage:
  skywire cli visor hv pk [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor pk

```

  Public key of the visor

Usage:
  skywire cli visor pk [flags]

Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from {/opt/skywire/apps /opt/skywire/local {/opt/skywire/users.db true}}
  -x, --prt string     serve public key via http (default "7998")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor info

```

  Summary of visor info

Usage:
  skywire cli visor info [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ver

```

  Version and build info

Usage:
  skywire cli visor ver [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ports

```

  List of all ports used by visor services and apps

Usage:
  skywire cli visor ports [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ip

```

  IP information of network

Usage:
  skywire cli visor ip [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ping

```

  Creates a route with the provided pk as a hop and returns latency on the conn

Usage:
  skywire cli visor ping <pk> [flags]

Flags:
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries (default 1)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor test

```

  Creates a route with public visors as a hop and returns latency on the conn

Usage:
  skywire cli visor test [flags]

Flags:
  -c, --count int   Count of Public Visors for using in test. (default 2)
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries per public visors (default 1)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor start

```
start visor

Usage:
  skywire cli visor start [flags]

Flags:
  -s, --src   'go run' external commands from the skywire sources

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor reload

```
reload visor

Usage:
  skywire cli visor reload [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor halt

```

  Stop a running visor

Usage:
  skywire cli visor halt [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor route

```

    View and set routing rules

Usage:
  skywire cli visor route [flags]

Available Commands:
  ls-rules                List routing rules
  rule                    Return routing rule by route ID key
  rm-rule                 Remove routing rule
  add-rule                Add routing rule

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route ls-rules

```

    List routing rules

Usage:
  skywire cli visor route ls-rules [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route rule

```

    Return routing rule by route ID key

Usage:
  skywire cli visor route rule <route-id> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route rm-rule

```

    Remove routing rule

Usage:
  skywire cli visor route rm-rule <route-id> [flags]

Flags:
  -a, --all   remove all routing rules

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route add-rule

```

    Add routing rule

Usage:
  skywire cli visor route add-rule ( app | fwd | intfwd ) [flags]

Available Commands:
  app                     Add app/consume routing rule
  fwd                     Add forward routing rule
  intfwd                  Add intermediary forward routing rule

Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route add-rule app

```

    Add routing rule

Usage:
  skywire cli visor route add-rule ( app | fwd | intfwd ) [flags]

Available Commands:
  app                     Add app/consume routing rule
  fwd                     Add forward routing rule
  intfwd                  Add intermediary forward routing rule

Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route add-rule fwd

```

    Add routing rule

Usage:
  skywire cli visor route add-rule ( app | fwd | intfwd ) [flags]

Available Commands:
  app                     Add app/consume routing rule
  fwd                     Add forward routing rule
  intfwd                  Add intermediary forward routing rule

Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor route add-rule intfwd

```

    Add routing rule

Usage:
  skywire cli visor route add-rule ( app | fwd | intfwd ) [flags]

Available Commands:
  app                     Add app/consume routing rule
  fwd                     Add forward routing rule
  intfwd                  Add intermediary forward routing rule

Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor tp

```

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg

Usage:
  skywire cli visor tp [flags]

Available Commands:
  type                    Transport types used by the local visor
  ls                      Available transports
  id                      Transport summary by id
  add                     Add a transport
  rm                      Remove transport(s) by id
  disc                    Discover remote transport(s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor tp type

```

  Transport types used by the local visor

Usage:
  skywire cli visor tp type

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor tp ls

```

    Available transports

    displays transports of the local visor

Usage:
  skywire cli visor tp ls [flags]

Flags:
  -t, --types strings   show transport(s) type(s) comma-separated
  -p, --pks strings     show transport(s) for public key(s) comma-separated
  -l, --logs            show transport logs (default true)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor tp id

```

    Transport summary by id

Usage:
  skywire cli visor tp id (-i) <transport-id>

Flags:
  -i, --id string   transport ID

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor tp add

```

    Add a transport

    If the transport type is unspecified,
    the visor will attempt to establish a transport
    in the following order: skywire-tcp, stcpr, sudph, dmsg

Usage:
  skywire cli visor tp add (-p) <remote-public-key>

Flags:
  -r, --rpk string         remote public key.
  -o, --timeout duration   if specified, sets an operation timeout
  -t, --type string        type of transport to add.

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor tp rm

```

    Remove transport(s) by id

Usage:
  skywire cli visor tp rm ( -a || -i ) <transport-id>

Flags:
  -a, --all         remove all transports
  -i, --id string   remove transport of given ID

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor tp disc

```

    Discover remote transport(s) by ID or public key

Usage:
  skywire cli visor tp disc (--id=<transport-id> || --pk=<edge-public-key>)

Flags:
  -i, --id string   obtain transport of given ID
  -p, --pk string   obtain transports by public key

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### cli vpn

```
VPN client

Usage:
  skywire cli vpn [flags]

Available Commands:
  start                   start the vpn for <public-key>
  stop                    stop the vpnclient
  status                  vpn client status
  list                    List servers
  ui                      Open VPN UI in default browser
  url                     Show VPN UI URL

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn start

```
start the vpn for <public-key>

Usage:
  skywire cli vpn start <public-key> [flags]

Flags:
  -k, --pk string     server public key
  -t, --timeout int   starting timeout value in second (default 20)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn stop

```
stop the vpnclient

Usage:
  skywire cli vpn stop [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn status

```
vpn client status

Usage:
  skywire cli vpn status [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn list

```
List vpn servers from service discovery
http://sd.skycoin.com/api/services?type=vpn
http://sd.skycoin.com/api/services?type=vpn&country=US

Set cache file location to "" to avoid using cache files

Usage:
  skywire cli vpn list [flags]

Flags:
  -m, --cfa int          update cache files if older than n minutes (default 5)
      --cfs string       SD cache file location (default "/tmp/vpnsd.json")
      --cfu string       UT cache file location. (default "/tmp/ut.json")
  -c, --country string   filter results by country
  -l, --label            label keys by country [91m(SLOW)[0m
  -o, --noton            do not filter by online status in UT
  -k, --pk string        check vpn service discovery for public key
  -r, --raw              print raw data
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
  -s, --stats            return only a count of the results
  -u, --unfilter         provide unfiltered results
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -v, --ver string       filter results by version

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn ui

```
Open VPN UI in default browser

Usage:
  skywire cli vpn ui [flags]

Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn url

```
Show VPN UI URL

Usage:
  skywire cli vpn url [flags]

Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### cli ut

```
query uptime tracker

http://ut.skywire.skycoin.com/uptimes?v=v2

Check local visor daily uptime percent with:
 skywire-cli ut -k $(skywire-cli visor pk)n
Set cache file location to "" to avoid using cache files

Usage:
  skywire cli ut [flags]

Flags:
  -m, --cfa int      update cache files if older than n minutes (default 5)
      --cfu string   UT cache file location. (default "/tmp/ut.json")
  -n, --min int      list visors meeting minimum uptime (default 75)
  -o, --on           list currently online visors
  -k, --pk string    check uptime for the specified key
  -s, --stats        count the number of results
  -u, --url string   specify alternative uptime tracker url (default "http://ut.skywire.skycoin.com")


```

#### cli fwd

```
Control skyforwarding
 forward local ports over skywire

Usage:
  skywire cli fwd [flags]

Flags:
  -d, --deregister   deregister local port of the external (http) app
  -l, --ls           list registered local ports
  -p, --port int     local port of the external (http) app


```

#### cli rev

```
connect or disconnect from remote ports

Usage:
  skywire cli rev [flags]

Flags:
  -l, --ls            list configured connections
  -k, --pk string     remote public key to connect to
  -p, --port int      local port to reverse proxy
  -r, --remote int    remote port to read from
  -d, --stop string   disconnect from specified <id>


```

#### cli reward

```

	reward address setting

	Sets the skycoin reward address for the visor.
	The config is written to the root of the default local directory

	this config is served via dmsghttp along with transport logs
	and the system hardware survey for automating reward distribution

Usage:
  skywire cli reward <address> || [flags]

Flags:
      --all   show all flags


```

#### cli rewards

```

Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt

Usage:
  skywire cli rewards [flags]

Flags:
  -d, --date string     date for which to calculate reward (default "2024-02-26")
  -k, --pk string       check reward for pubkey
  -n, --noarch string   disallowed architectures, comma separated (default "amd64")
  -y, --year int        yearly total rewards (default 408000)
  -u, --utfile string   uptime tracker data file (default "ut.txt")
  -p, --path string     path to the surveys (default "log_collecting")
  -0, --h0              hide statistical data
  -1, --h1              hide survey csv data
  -2, --h2              hide reward csv data
  -e, --err             account for non rewarded keys


```

#### cli survey

```
print the system survey

Usage:
  skywire cli survey

Flags:
  -s, --sha   generate checksum of system survey


```

```
unknown command "survey" for "skywire"

```

#### cli rtfind

```
Query the Route Finder
Assumes the local visor public key as an argument if only one argument is given

Usage:
  skywire cli rtfind <public-key> | <public-key-visor-1> <public-key-visor-2> [flags]

Flags:
  -n, --min uint16         minimum hops (default 1)
  -x, --max uint16         maximum hops (default 1000)
  -t, --timeout duration   request timeout (default 10s)
  -a, --addr string        route finder service address
                           http://rf.skywire.skycoin.com


```

#### cli rtree

```
display a tree representation of transports from TPD

http://tpd.skywire.skycoin.com/all-transports

Set cache file location to "" to avoid using cache files

Usage:
  skywire cli rtree [flags]

Flags:
  -m, --cfa int         update cache files if older than n minutes (default 5)
      --cft string      TPD cache file location (default "/tmp/tpd.json")
      --cfu string      UT cache file location. (default "/tmp/ut.json")
  -o, --noton           do not filter by online status in UT
  -P, --pad int         padding between tree and tpid (default 15)
  -p, --pretty          print pretty json data
  -r, --raw             print raw json data
  -s, --stats           return only statistics
  -a, --tpdurl string   transport discovery url (default "http://tpd.skywire.skycoin.com")
  -w, --uturl string    uptime tracker url (default "http://ut.skywire.skycoin.com")


```

#### cli mdisc

```
Query remote DMSG Discovery

Usage:
  skywire cli mdisc

Available Commands:
  entry                   Fetch an entry
  servers                 Fetch available servers


```

##### cli mdisc entry

```
Fetch an entry

Usage:
  skywire cli mdisc entry <visor-public-key> [flags]

Flags:
  -a, --addr string   DMSG discovery server address
                      http://dmsgd.skywire.skycoin.com


```

##### cli mdisc servers

```
Fetch available servers

Usage:
  skywire cli mdisc servers [flags]

Flags:
      --addr string   address of DMSG discovery server
                       (default "http://dmsgd.skywire.skycoin.com")


```

#### cli completion

```
Generate completion script

Usage:
  skywire cli completion [bash|zsh|fish|powershell]


```

#### cli log

```
Fetch health, survey, and transport logging from visors which are online in the uptime tracker
http://ut.skywire.skycoin.com/uptimes?v=v2
http://ut.skywire.skycoin.com/uptimes?v=v2&visors=<pk1>;<pk2>;<pk3>

Usage:
  skywire cli log [flags]

Flags:
  -e, --env string                deployment to get uptimes from (default "prod")
  -l, --log                       fetch only transport logs
  -v, --survey                    fetch only surveys
  -f, --file string               fetch only a specific file from all online visors
  -k, --pks string                fetch only from specific public keys ; semicolon separated
  -d, --dir string                save files to specified dir (default "log_collecting")
  -c, --clean                     delete files and folders on errors
      --minv string               minimum visor version to fetch from (default "v1.3.15")
      --include-versions string   list of version that not satisfy our minimum version condition, but we want include them
  -n, --duration int              number of days before today to fetch transport logs for
      --all                       consider all visors ; no version filtering
      --batchSize int             number of visor in each batch (default 50)
      --maxfilesize int           maximum file size allowed to download during collecting logs, in KB (default 1024)
  -D, --dmsg-disc string          dmsg discovery url
                                   (default "http://dmsgd.skywire.skycoin.com")
  -u, --ut string                 custom uptime tracker url
  -s, --sk cipher.SecKey          a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)


```

#### cli proxy

```
Skysocks client

Usage:
  skywire cli proxy [flags]

Available Commands:
  start                   start the proxy client
  stop                    stop the proxy client
stop the default instance with:
 stop --name skysocks-client
  status                  proxy client status
  list                    List servers

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy start

```
start the proxy client

Usage:
  skywire cli proxy start [flags]

Flags:
  -a, --addr string         address of proxy for use
  -p, --http-proxy string   starting http-proxy based on skysocks
  -n, --name string         name of skysocks client
  -k, --pk string           server public key
  -t, --timeout int         starting timeout value in second (default 20)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy stop

```
stop the proxy client
stop the default instance with:
 stop --name skysocks-client

Usage:
  skywire cli proxy stop [flags]

Flags:
      --all           stop all skysocks client
      --name string   specific skysocks client that want stop

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy status

```
proxy client status

Usage:
  skywire cli proxy status [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy list

```
List proxy servers from service discovery
http://sd.skycoin.com/api/services?type=proxy
http://sd.skycoin.com/api/services?type=proxy&country=US

Set cache file location to "" to avoid using cache files

Usage:
  skywire cli proxy list [flags]

Flags:
  -m, --cfa int          update cache files if older than n minutes (default 5)
      --cfs string       SD cache file location (default "/tmp/proxysd.json")
      --cfu string       UT cache file location. (default "/tmp/ut.json")
  -c, --country string   filter results by country
  -l, --label            label keys by country [91m(SLOW)[0m
  -o, --noton            do not filter by online status in UT
  -k, --pk string        check proxy service discovery for public key
  -r, --raw              print raw data
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
  -s, --stats            return only a count of the results
  -u, --unfilter         provide unfiltered results
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -v, --ver string       filter results by version

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### cli tree

```
subcommand tree

Usage:
  skywire cli tree


```

#### cli doc

```
generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc > cmd/skywire-cli/README1.md

	generate toc:

	cat cmd/skywire-cli/README1.md | gh-md-toc

Usage:
  skywire cli doc


```

### svc

```

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘

Available Commands:
  sn                      Route Setup Node for skywire
  tpd                     Transport Discovery Server for skywire
  tps                     Transport setup server for skywire
  ar                      Address Resolver Server for skywire
  rf                      Route Finder Server for skywire
  cb                      Config Bootstrap Server for skywire
  kg                      skywire keys generator, prints pub-key and sec-key
  lc                      Liveness checker of the deployment.
  nv                      Node Visualizer Server for skywire
  swe                     skywire environment generator
  sd                      Service discovery server
  mn                      Network monitor for skywire VPN and Visor.
  pvm                     Public Visor monitor.
  ssm                     Skysocks monitor.
  vpnm                    VPN monitor.


```

#### svc sn

```

	┌─┐┌─┐┌┬┐┬ ┬┌─┐   ┌┐┌┌─┐┌┬┐┌─┐
	└─┐├┤  │ │ │├─┘───││││ │ ││├┤
	└─┘└─┘ ┴ └─┘┴     ┘└┘└─┘─┴┘└─┘


```

#### svc tpd

```

	┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐ ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴



Flags:
  -a, --addr string             address to bind to[0m (default ":9091")
      --dmsg-disc string        url of dmsg-discovery[0m (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16         dmsg port value
 (default 80)
  -l, --loglvl string           set log level one of: info, error, warn, debug, trace, panic (default "info")
  -m, --metrics string          address to bind metrics API to[0m
      --pg-host string          host of postgres[0m (default "localhost")
      --pg-port string          port of postgres[0m (default "5432")
      --redis string            connections string for a redis store[0m (default "redis://localhost:6379")
      --redis-pool-size int     redis connection pool size[0m (default 10)
      --sk cipher.SecKey        dmsg secret key
 (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string           syslog server address. E.g. localhost:514[0m
      --tag string              logging tag[0m (default "transport_discovery")
      --test-environment        distinguished between prod and test environment[0m
  -t, --testing                 enable testing to start without redis[0m
      --whitelist-keys string   list of whitelisted keys of network monitor used for deregistration[0m


```

#### svc tps

```

	┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐  ┌─┐┌─┐┌┬┐┬ ┬┌─┐
	 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │───└─┐├┤  │ │ │├─┘
	 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴   └─┘└─┘ ┴ └─┘┴



Flags:
  -c, --config string   path to config file[0m
  -l, --loglvl string   set log level one of: info, error, warn, debug, trace, panic (default "info")


```

#### svc ar

```

	┌─┐┌┬┐┌┬┐┬─┐┌─┐┌─┐┌─┐   ┬─┐┌─┐┌─┐┌─┐┬ ┬  ┬┌─┐┬─┐
	├─┤ ││ ││├┬┘├┤ └─┐└─┐───├┬┘├┤ └─┐│ ││ └┐┌┘├┤ ├┬┘
	┴ ┴─┴┘─┴┘┴└─└─┘└─┘└─┘   ┴└─└─┘└─┘└─┘┴─┘└┘ └─┘┴└─



Flags:
  -a, --addr string             address to bind to[0m (default ":9093")
      --dmsg-disc string        url of dmsg-discovery[0m (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16         dmsg port value
 (default 80)
  -l, --loglvl string           set log level one of: info, error, warn, debug, trace, panic (default "info")
  -m, --metrics string          address to bind metrics API to[0m
      --redis string            connections string for a redis store[0m (default "redis://localhost:6379")
      --redis-pool-size int     redis connection pool size[0m (default 10)
      --sk cipher.SecKey        dmsg secret key
 (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string           syslog server address. E.g. localhost:514[0m
      --tag string              logging tag[0m (default "address_resolver")
      --test-environment        distinguished between prod and test environment[0m
  -t, --testing                 enable testing to start without redis[0m
      --whitelist-keys string   list of whitelisted keys of network monitor used for deregistration[0m


```

#### svc rf

```

	┬─┐┌─┐┬ ┬┌┬┐┌─┐  ┌─┐┬┌┐┌┌┬┐┌─┐┬─┐
	├┬┘│ ││ │ │ ├┤───├┤ ││││ ││├┤ ├┬┘
	┴└─└─┘└─┘ ┴ └─┘  └  ┴┘└┘─┴┘└─┘┴└─



Flags:
  -a, --addr string        address to bind to[0m (default ":9092")
      --dmsg-disc string   url of dmsg-discovery[0m (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16    dmsg port value
 (default 80)
  -l, --loglvl string      set log level one of: info, error, warn, debug, trace, panic (default "info")
  -m, --metrics string     address to bind metrics API to[0m
      --pg-host string     host of postgres[0m (default "localhost")
      --pg-port string     port of postgres[0m (default "5432")
      --sk cipher.SecKey   dmsg secret key
 (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string      syslog server address. E.g. localhost:514[0m
      --tag string         logging tag[0m (default "route_finder")
  -t, --testing            enable testing to start without redis[0m


```

#### svc cb

```

	┌─┐┌─┐┌┐┌┌─┐┬┌─┐   ┌┐ ┌─┐┌─┐┌┬┐┌─┐┌┬┐┬─┐┌─┐┌─┐┌─┐┌─┐┬─┐
	│  │ ││││├┤ ││ ┬───├┴┐│ ││ │ │ └─┐ │ ├┬┘├─┤├─┘├─┘├┤ ├┬┘
	└─┘└─┘┘└┘└  ┴└─┘   └─┘└─┘└─┘ ┴ └─┘ ┴ ┴└─┴ ┴┴  ┴  └─┘┴└─



Flags:
  -a, --addr string        address to bind to[0m (default ":9082")
  -c, --config string      stun server list file location[0m (default "./config.json")
      --dmsg-disc string   url of dmsg-discovery[0m (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16    dmsg port value
 (default 80)
  -d, --domain string      the domain of the endpoints[0m (default "skywire.skycoin.com")
      --sk cipher.SecKey   dmsg secret key
 (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string         logging tag[0m (default "address_resolver")


```

#### svc kg

```

	┬┌─┌─┐┬ ┬┌─┐   ┌─┐┌─┐┌┐┌
	├┴┐├┤ └┬┘└─┐───│ ┬├┤ │││
	┴ ┴└─┘ ┴ └─┘   └─┘└─┘┘└┘




```

#### svc lc

```

	┬  ┬┬  ┬┌─┐┌┐┌┌─┐┌─┐┌─┐   ┌─┐┬ ┬┌─┐┌─┐┬┌─┌─┐┬─┐
	│  │└┐┌┘├┤ │││├┤ └─┐└─┐───│  ├─┤├┤ │  ├┴┐├┤ ├┬┘
	┴─┘┴ └┘ └─┘┘└┘└─┘└─┘└─┘   └─┘┴ ┴└─┘└─┘┴ ┴└─┘┴└─



Flags:
  -a, --addr string     address to bind to.[0m (default ":9081")
  -c, --config string   config file location.[0m (default "liveness-checker.json")
  -l, --loglvl string   set log level one of: info, error, warn, debug, trace, panic (default "info")
      --redis string    connections string for a redis store[0m (default "redis://localhost:6379")
      --syslog string   syslog server address. E.g. localhost:514[0m
      --tag string      logging tag[0m (default "liveness_checker")
  -t, --testing         enable testing to start without redis[0m


```

#### svc nv

```

	┌┐┌┌─┐┌┬┐┌─┐  ┬  ┬┬┌─┐┬ ┬┌─┐┬  ┬┌─┐┌─┐┬─┐
	││││ │ ││├┤───└┐┌┘│└─┐│ │├─┤│  │┌─┘├┤ ├┬┘
	┘└┘└─┘─┴┘└─┘   └┘ ┴└─┘└─┘┴ ┴┴─┘┴└─┘└─┘┴└─



Flags:
  -a, --addr string      address to bind to[0m (default ":9081")
  -l, --log              enable request logging[0m (default true)
  -m, --metrics string   address to bind metrics API to[0m
      --syslog string    syslog server address. E.g. localhost:514[0m
      --tag string       logging tag[0m (default "node-visualizer")
  -t, --testing          enable testing to start without redis[0m


```

#### svc swe

```

	┌─┐┬ ┬   ┌─┐┌┐┌┬  ┬
	└─┐│││───├┤ │││└┐┌┘
	└─┘└┴┘   └─┘┘└┘ └┘

Available Commands:
  visor                   Generate config for skywire-visor
  dmsg                    Generate config for dmsg-server
  setup                   Generate config for setup node

Flags:
  -d, --docker           Environment with dockerized skywire-services[0m
  -l, --local            Environment with skywire-services on localhost[0m
  -n, --network string   Docker network to use[0m (default "SKYNET")
  -p, --public           Environment with public skywire-services[0m


```

##### svc swe visor

```
Generate config for skywire-visor




```

##### svc swe dmsg

```
Generate config for dmsg-server




```

##### svc swe setup

```
Generate config for setup node




```

#### svc sd

```

	┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐ ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	└─┐├┤ ├┬┘└┐┌┘││  ├┤───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	└─┘└─┘┴└─ └┘ ┴└─┘└─┘ ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴



Flags:
  -a, --addr string             address to bind to (default ":9098")
  -g, --api-key string          geo API key
  -d, --dmsg-disc string        url of dmsg-discovery default:
                                http://dmsgd.skywire.skycoin.com
      --dmsgPort uint16         dmsg port value
 (default 80)
  -m, --metrics string          address to bind metrics API to
  -o, --pg-host string          host of postgres (default "localhost")
  -p, --pg-port string          port of postgres (default "5432")
  -r, --redis string            connections string for a redis store (default "redis://localhost:6379")
  -s, --sk cipher.SecKey        dmsg secret key
                                 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --test                    run in test mode and disable auth
  -n, --test-environment        distinguished between prod and test environment
  -w, --whitelist-keys string   list of whitelisted keys of network monitor used for deregistration


```

#### svc mn

```

	┌┐┌┌─┐┌┬┐┬ ┬┌─┐┬─┐┬┌─   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	│││├┤  │ ││││ │├┬┘├┴┐───││││ │││││ │ │ │├┬┘
	┘└┘└─┘ ┴ └┴┘└─┘┴└─┴ ┴   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─



Flags:
  -a, --addr string                     address to bind to.[0m (default ":9080")
  -v, --ar-url string                   url to address resolver.[0m
  -b, --batchsize int                   Batch size of deregistration[0m (default 30)
  -c, --config string                   config file location.[0m (default "network-monitor.json")
  -l, --loglvl string                   set log level one of: info, error, warn, debug, trace, panic (default "info")
  -m, --metrics string                  address to bind metrics API to[0m
      --redis string                    connections string for a redis store[0m (default "redis://localhost:6379")
      --redis-pool-size int             redis connection pool size[0m (default 10)
  -n, --sd-url string                   url to service discovery.[0m
      --sleep-deregistration duration   Sleep time for derigstration process in minutes[0m (default 10ns)
      --syslog string                   syslog server address. E.g. localhost:514[0m
      --tag string                      logging tag[0m (default "network_monitor")
  -t, --testing                         enable testing to start without redis[0m
  -u, --ut-url string                   url to uptime tracker visor data.[0m


```

#### svc pvm

```

	┌─┐┬ ┬┌┐ ┬  ┬┌─┐ ┬  ┬┬┌─┐┌─┐┬─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	├─┘│ │├┴┐│  ││───└┐┌┘│└─┐│ │├┬┘───││││ │││││ │ │ │├┬┘
	┴  └─┘└─┘┴─┘┴└─┘  └┘ ┴└─┘└─┘┴└─   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─



Flags:
  -a, --addr string                     address to bind to.[0m (default ":9082")
  -c, --config string                   config file location.[0m (default "public-visor-monitor.json")
  -l, --loglvl string                   set log level one of: info, error, warn, debug, trace, panic (default "info")
  -s, --sleep-deregistration duration   Sleep time for derigstration process in minutes[0m (default 10ns)
      --tag string                      logging tag[0m (default "public_visor_monitor")


```

#### svc ssm

```

	┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐───││││ │││││ │ │ │├┬┘
	└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─



Flags:
  -a, --addr string                     address to bind to.[0m (default ":9081")
  -c, --config string                   config file location.[0m (default "skysocks-monitor.json")
  -s, --sleep-deregistration duration   Sleep time for derigstration process in minutes[0m (default 10ns)
      --tag string                      logging tag[0m (default "skysocks_monitor")


```

#### svc vpnm

```

	┬  ┬┌─┐┌┐┌   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	└┐┌┘├─┘│││───││││ │││││ │ │ │├┬┘
	 └┘ ┴  ┘└┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─



Flags:
  -a, --addr string                     address to bind to.[0m (default ":9081")
  -c, --config string                   config file location.[0m (default "vpn-monitor.json")
  -s, --sleep-deregistration duration   Sleep time for derigstration process in minutes[0m (default 10ns)
      --tag string                      logging tag[0m (default "vpn_monitor")


```

### dmsg

```

	┌┬┐┌┬┐┌─┐┌─┐
	 │││││└─┐│ ┬
	─┴┘┴ ┴└─┘└─┘

Available Commands:
  pty                     Dmsg pseudoterminal (pty)
  disc                    DMSG Discovery Server
  server                  DMSG Server
  http                    DMSG http file server
  curl                    DMSG curl utility
  web                     DMSG resolving proxy & browser client
  proxy                   
  mon                     DMSG monitor of DMSG discoery.


```

#### dmsg pty

```

	┌─┐┌┬┐┬ ┬
	├─┘ │ └┬┘
	┴   ┴  ┴

Available Commands:
  cli                     DMSG pseudoterminal command line interface
  host                    DMSG host for pseudoterminal command line interface
  ui                      DMSG pseudoterminal GUI


```

##### dmsg pty cli

```

	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┌─┐┬  ┬
	 │││││└─┐│ ┬├─┘ │ └┬┘───│  │  │
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    └─┘┴─┘┴
  DMSG pseudoterminal command line interface

Available Commands:
  whitelist                    lists all whitelisted public keys
  whitelist-add                adds public key(s) to the whitelist
  whitelist-remove             removes public key(s) from the whitelist

Flags:
      --addr dmsg.Addr    remote dmsg address of format 'pk:port'
                           If unspecified, the pty will start locally
                           (default 000000000000000000000000000000000000000000000000000000000000000000:~)
  -a, --args strings      command arguments
  -r, --cliaddr string    address to use for dialing to dmsgpty-host (default "/tmp/dmsgpty.sock")
  -n, --clinet string     network to use for dialing to dmsgpty-host (default "unix")
  -c, --cmd string        name of command to run
                           (default "/bin/bash")
  -p, --confpath string   config path (default "config.json")


```

###### dmsg pty cli whitelist

```
lists all whitelisted public keys




```

###### dmsg pty cli whitelist-add

```
adds public key(s) to the whitelist




```

###### dmsg pty cli whitelist-remove

```
removes public key(s) from the whitelist




```

##### dmsg pty host

```

	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┌─┐┌─┐┌┬┐
	 │││││└─┐│ ┬├─┘ │ └┬┘───├─┤│ │└─┐ │
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    ┴ ┴└─┘└─┘ ┴
  DMSG host for pseudoterminal command line interface

Available Commands:
  confgen                 generates config file

Flags:
      --cliaddr string      address used for listening for cli connections (default "/tmp/dmsgpty.sock")
      --clinet string       network used for listening for cli connections (default "unix")
  -c, --confpath string     config path (default "./config.json")
      --confstdin           config will be read from stdin if set
      --dmsgdisc string     dmsg discovery address (default "http://dmsgd.skywire.skycoin.com")
      --dmsgport uint16     dmsg port for listening for remote hosts (default 22)
      --dmsgsessions int    minimum number of dmsg sessions to ensure (default 1)
      --envprefix string    env prefix (default "DMSGPTY")
      --wl cipher.PubKeys   whitelist of the dmsgpty-host (default public keys:
                            )


```

###### dmsg pty host confgen

```
generates config file



Flags:
      --unsafe   will unsafely write config if set


```

##### dmsg pty ui

```

	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┬
	 │││││└─┐│ ┬├─┘ │ └┬┘───│ ││
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    └─┘┴
  DMSG pseudoterminal GUI



Flags:
      --addr string       network address to serve UI on (default ":8080")
      --arg stringArray   command arguments to include when initiating pty
      --cmd string        command to run when initiating pty (default "/bin/bash")
      --haddr string      dmsgpty host network address (default "/tmp/dmsgpty.sock")
      --hnet string       dmsgpty host network name (default "unix")


```

#### dmsg disc

```

	┌┬┐┌┬┐┌─┐┌─┐  ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │││││└─┐│ ┬───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	─┴┘┴ ┴└─┘└─┘  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
  DMSG Discovery Server



Flags:
  -a, --addr string               address to bind to (default ":9090")
      --auth string               auth passphrase as simple auth for official dmsg servers registration
      --dmsgPort uint16           dmsg port value
 (default 80)
      --enable-load-testing       enable load testing
      --entry-timeout duration    discovery entry timeout (default 3m0s)
  -m, --metrics string            address to serve metrics API from
      --official-servers string   list of official dmsg servers keys separated by comma
      --redis string              connections string for a redis store (default "redis://localhost:6379")
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string             address in which to dial to syslog server
      --syslog-lvl string         minimum log level to report (default "debug")
      --syslog-net string         network in which to dial to syslog server (default "udp")
      --tag string                tag used for logging and metrics (default "dmsg_disc")
      --test-environment          distinguished between prod and test environment
  -t, --test-mode                 in testing mode
      --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration


```

#### dmsg server

```

	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	││││││└─┐│ ┬ ─ └─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘┴└─ └┘ └─┘┴└─
  DMSG Server

Available Commands:
  config                  Generate a dmsg-server config
  start                   Start Dmsg Server


```

##### dmsg server config

```
Generate a dmsg-server config

Available Commands:
  gen                     Generate a config file


```

###### dmsg server config gen

```
Generate a config file



Flags:
  -o, --output string   config output path/name
  -t, --testenv         use test deployment


```

##### dmsg server start

```
Start Dmsg Server



Flags:
      --auth string         auth passphrase as simple auth for official dmsg servers registration
  -c, --config string       location of config file (STDIN to read from standard input) (default "config.json")
      --limit-ip int        set limitation of IPs want connect to specific dmsg-server, default value is 15 (default 15)
  -m, --metrics string      address to serve metrics API from
      --stdin               whether to read config via stdin
      --syslog string       address in which to dial to syslog server
      --syslog-lvl string   minimum log level to report (default "debug")
      --syslog-net string   network in which to dial to syslog server (default "udp")
      --tag string          tag used for logging and metrics (default "dmsg_srv")


```

#### dmsg http

```

	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌┬┐┌┬┐┌─┐
	 │││││└─┐│ ┬├─┤ │  │ ├─┘
	─┴┘┴ ┴└─┘└─┘┴ ┴ ┴  ┴ ┴
  DMSG http file server



Flags:
  -d, --dir string         local dir to serve via dmsghttp (default ".")
  -D, --dmsg-disc string   dmsg discovery url default:
                           http://dmsgd.skywire.skycoin.com
  -p, --port uint          dmsg port to serve from (default 80)
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl string          whitelist keys, comma separated


```

#### dmsg curl

```

	┌┬┐┌┬┐┌─┐┌─┐┌─┐┬ ┬┬─┐┬
	 │││││└─┐│ ┬│  │ │├┬┘│
	─┴┘┴ ┴└─┘└─┘└─┘└─┘┴└─┴─┘
  DMSG curl utility



Flags:
  -a, --agent AGENT        identify as AGENT (default "dmsgcurl/unknown")
  -d, --data string        dmsghttp POST data
  -c, --dmsg-disc string   dmsg discovery url default:
                           http://dmsgd.skywire.skycoin.com
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m (default "fatal")
  -o, --out string         output filepath
  -r, --replace            replace exist file with new downloaded
  -e, --sess int           number of dmsg servers to connect to (default 1)
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --try int            download attempts (0 unlimits) (default 1)
  -w, --wait int           time to wait between fetches


```

#### dmsg web

```

	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌─┐┌┐
	 │││││└─┐│ ┬│││├┤ ├┴┐
	─┴┘┴ ┴└─┘└─┘└┴┘└─┘└─┘
  DMSG resolving proxy & browser client - access websites over dmsg

Available Commands:
  gen-keys                generate public / secret keypair

Flags:
  -d, --dmsg-disc string   dmsg discovery url default:
                           http://dmsgd.skywire.skycoin.com
  -f, --filter string      domain suffix to filter (default ".dmsg")
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
  -p, --port string        port to serve the web application (default "8080")
  -r, --proxy string       configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)
  -t, --resolve string     resolve the specified dmsg address:port on the local port & disable proxy
  -e, --sess int           number of dmsg servers to connect to (default 1)
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -q, --socks string       port to serve the socks5 proxy (default "4445")


```

##### dmsg web gen-keys

```
generate public / secret keypair




```

#### dmsg proxy

```
Available Commands:
  server                  dmsg proxy server
  client                  socks5 proxy to connect to socks5 server over dmsg


```

##### dmsg proxy server

```
dmsg proxy server



Flags:
  -D, --dmsg-disc string   dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
  -q, --dport uint16       dmsg port to serve socks5 (default 1081)
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl string          whitelist keys, comma separated


```

##### dmsg proxy client

```
socks5 proxy to connect to socks5 server over dmsg



Flags:
  -D, --dmsg-disc string   dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
  -q, --dport uint16       dmsg port to connect to socks5 server (default 1081)
  -k, --pk string          dmsg socks5 proxy server public key to connect to
  -p, --port int           TCP port to serve SOCKS5 proxy locally (default 1081)
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)


```

#### dmsg mon

```

	┌┬┐┌┬┐┌─┐┌─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	 │││││└─┐│ ┬───││││ │││││ │ │ │├┬┘
	─┴┘┴ ┴└─┘└─┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─



Flags:
  -a, --addr string                     address to bind to.[0m (default ":9080")
  -b, --batchsize int                   Batch size of deregistration[0m (default 20)
  -c, --config string                   config file location.[0m (default "dmsg-monitor.json")
  -d, --dmsg-url string                 url to dmsg data.[0m
  -l, --loglvl string                   set log level one of: info, error, warn, debug, trace, panic (default "info")
  -s, --sleep-deregistration duration   Sleep time for derigstration process in minutes[0m (default 10ns)
      --syslog string                   syslog server address. E.g. localhost:514[0m
      --tag string                      logging tag[0m (default "dmsg_monitor")
  -u, --ut-url string                   url to uptime tracker visor data.[0m


```

### app

```

	┌─┐┌─┐┌─┐┌─┐
	├─┤├─┘├─┘└─┐
	┴ ┴┴  ┴  └─┘

Available Commands:
  vpn-server                  skywire vpn server application
  vpn-client                  skywire vpn client application
  skysocks-client             skywire socks5 proxy client application
  skysocks                    skywire socks5 proxy server application
  skychat                     skywire chat application


```

#### app vpn-server

```

	┬  ┬┌─┐┌┐┌   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	└┐┌┘├─┘│││───└─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
 	 └┘ ┴  ┘└┘   └─┘└─┘┴└─ └┘ └─┘┴└─



Flags:
      --netifc string     Default network interface for multiple available interfaces
      --passcode string   passcode to authenticate connecting users
      --pk string         local pubkey
      --secure            Forbid connections from clients to server local network (default true)
      --sk string         local seckey


```

#### app vpn-client

```

	┬  ┬┌─┐┌┐┌   ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
	└┐┌┘├─┘│││───│  │  │├┤ │││ │
 	 └┘ ┴  ┘└┘   └─┘┴─┘┴└─┘┘└┘ ┴



Flags:
      --dns string        address of DNS want set to tun
      --killswitch        If set, the Internet won't be restored during reconnection attempts
      --passcode string   passcode to authenticate connection
      --pk string         local pubkey
      --sk string         local seckey
      --srv string        PubKey of the server to connect to


```

#### app skysocks-client

```

	┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐   ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
	└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐───│  │  │├┤ │││ │
	└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘   └─┘┴─┘┴└─┘┘└┘ ┴



Flags:
      --addr string   Client address to listen on (default ":1080")
      --http string   http proxy mode
      --srv string    PubKey of the server to connect to


```

#### app skysocks

```

	┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐
	└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐
	└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘



Flags:
      --passcode string   passcode to authenticate connecting users


```

#### app skychat

```

	┌─┐┬┌─┬ ┬┌─┐┬ ┬┌─┐┌┬┐
	└─┐├┴┐└┬┘│  ├─┤├─┤ │
	└─┘┴ ┴ ┴ └─┘┴ ┴┴ ┴ ┴



Flags:
      --addr string   address to bind, put an * before the port if you want to be able to access outside localhost (default ":8001")


```

### tree

```
subcommand tree




```

### doc

```
generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-deployment/skywire.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-deployment/skywire.go doc > cmd/skywire-deployment/README1.md

	generate toc:

	cat cmd/skywire-deployment/README1.md | gh-md-toc




```

###

```

```
