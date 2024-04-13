# Skywire Merged Binary


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
  │ │ └──halt
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
  │ ├─┬rewards
  │ │ └──ui
  │ ├──survey
  │ ├─┬route
  │ │ ├──rm
  │ │ ├─┬add
  │ │ │ ├──a
  │ │ │ ├──b
  │ │ │ └──c
  │ │ └──find
  │ ├─┬tp
  │ │ ├──add
  │ │ ├──rm
  │ │ ├──disc
  │ │ └──tree
  │ ├─┬mdisc
  │ │ ├──entry
  │ │ └──servers
  │ ├──completion
  │ ├─┬log
  │ │ ├──st
  │ │ └──tp
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
  │ ├─┬tps
  │ │ ├──add
  │ │ ├──rm
  │ │ └──list
  │ ├──ar
  │ ├──rf
  │ ├──cb
  │ ├──kg
  │ ├──lc
  │ ├──nv
  │ ├─┬se
  │ │ ├──visor
  │ │ ├──dmsg
  │ │ └──setup
  │ ├──sd
  │ ├──nwmon
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
  │ ├─┬socks
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
  └──doc

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
  route                   View and set rules
  tp                      View and manage transports
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

Available Commands:
  gen                     Generate a config file
  gen-keys                generate public / secret keypair
  check-pk                check a skywire public key
  update                  Update a config file


```

##### cli config gen

```
Generate a config file

	Config defaults file may also be specified with:
	SKYENV=/path/to/skywire.conf skywire-cli config gen
	print the SKYENV file template with:
	skywire-cli config gen -q



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
      --binpath string           set bin_path for visor vative apps[0m
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
  -S, --svcconf string           fallback service configuration file[0m (default "services-config.json")
      --nodefaults               do not use hardcoded defaults for production / test services
      --sn                       generate config for route setup-node
      --version string           custom version testing override[0m
      --all                      show all flags


```

##### Example for package / msi

```
$ skywire cli config gen -bpirxn
{
	"version": "v1.3.20",
	"sk": "edbfbaaf1f4341e0536ba26249a8ee5d372699dab7be53aa9b38f335e479ce67",
	"pk": "038a737f250cbbb077bca10c2ed5b2ba698bb3ffa72c412e095a92e01b72bf4b6b",
	"dmsg": {
		"discovery": "http://dmsgd.skywire.skycoin.com",
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
	"persistent_transports": null,
	"hypervisor": {
		"db_path": "/opt/skywire/users.db",
		"enable_auth": true,
		"cookies": {
			"hash_key": "acce5f29bc86e280ecd58b78fc75401e1cfb94c3b613ed2fd6522d934cfb4f06c4ef932d7aec3b6d1c9a4974a2829bc464644731afb03412ecda2806502ea436",
			"block_key": "c641019012c9313eb71800d9d7561edb93d393b8d2957ebb050d9864911c9477",
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




```

##### cli config check-pk

```
check a skywire public key




```

##### cli config update

```
Update a config file

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

Available Commands:
  ui                      Open dmsgpty UI in default browser
  url                     Show dmsgpty UI URL
  list                    List connected visors
  start                   Start dmsgpty session


```

##### cli dmsgpty ui

```
Open dmsgpty UI in default browser



Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

##### cli dmsgpty url

```
Show dmsgpty UI URL



Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

##### cli dmsgpty list

```
List connected visors



Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli dmsgpty start

```
Start dmsgpty session



Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")
      --rpc string    RPC server address (default "localhost:3435")


```

#### cli visor

```
Query the Skywire Visor

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

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor app

```

  App settings

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



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app start

```

  Launch app



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app stop

```

  Halt app



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app register

```

  Register app



Flags:
  -a, --appname string     name of the app
  -p, --localpath string   path of the local folder (default "./local")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app deregister

```

  Deregister app



Flags:
  -k, --procKey string   proc key of the app to deregister

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app log

```

  Logs from app since RFC3339Nano-formatted timestamp.


  "beginning" is a special timestamp to fetch all the logs



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor app arg

```
App args

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



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### cli visor hv cpk

```

  Public key of remote hypervisor(s) set in config



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



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor pk

```

  Public key of the visor



Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from {/opt/skywire/bin /opt/skywire/local {/opt/skywire/users.db true}}
  -x, --prt string     serve public key via http (default "7998")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor info

```

  Summary of visor info



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ver

```

  Version and build info



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ports

```

  List of all ports used by visor services and apps



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ip

```

  IP information of network



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor ping

```

  Creates a route with the provided pk as a hop and returns latency on the conn



Flags:
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries (default 1)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor test

```

  Creates a route with public visors as a hop and returns latency on the conn



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



Flags:
  -s, --src   'go run' external commands from the skywire sources

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor reload

```
reload visor



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli visor halt

```

  Stop a running visor



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### cli vpn

```
VPN client

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



Flags:
  -k, --pk string     server public key
  -t, --timeout int   starting timeout value in second

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn stop

```
stop the vpnclient



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn status

```
vpn client status



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn list

```
List vpn servers from service discovery
http://sd.skycoin.com/api/services?type=vpn
http://sd.skycoin.com/api/services?type=vpn&country=US

Set cache file location to "" to avoid using cache files



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



Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli vpn url

```
Show VPN UI URL



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



Flags:
  -d, --deregister   deregister local port of the external (http) app
  -l, --ls           list registered local ports
  -p, --port int     local port of the external (http) app


```

#### cli rev

```
connect or disconnect from remote ports



Flags:
  -l, --ls            list configured connections
  -k, --pk string     remote public key to connect to
  -p, --port int      local port to reverse proxy
  -r, --remote int    remote port to read from
  -d, --stop string   disconnect from specified <id>


```

#### cli reward

```

    skycoin reward address set to:



Flags:
      --all   show all flags


```

#### cli rewards

```

Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt

Available Commands:
  ui                      reward system user interface

Flags:
  -d, --date string     date for which to calculate reward (default "2024-04-12")
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

##### cli rewards ui

```
skycoin reward system and skywire network metrics: https://fiber.skywire.dev

	┌─┐┬┌┐ ┌─┐┬─┐
	├┤ │├┴┐├┤ ├┬┘
	└  ┴└─┘└─┘┴└─
	run the web application

.conf file may also be specified with
SKYENV=/path/to/fiber.conf fiber run



Flags:
  -D, --dmsg-disc string       dmsg discovery url default:
                               http://dmsgd.skywire.skycoin.com
  -d, --dport uint             dmsg port to serve (default 80)
  -e, --dsess int              dmsg sessions (default 1)
  -O, --ensure-online string   Exit when the specified URL cannot be fetched;
                               i.e. https://fiber.skywire.dev
  -p, --port uint              port to serve (default 80)
  -s, --sk cipher.SecKey       a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl string              add whitelist keys, comma separated to permit POST of reward transaction to be broadcast


```

#### cli survey

```
print the system survey



Flags:
  -s, --sha   generate checksum of system survey


```

```
unknown command "survey" for "skywire"

```

#### cli route

```

    View and set routing rules

Available Commands:
  rm                      Remove routing rule
  add                     Add routing rule
  find                    Query the Route Finder

Flags:
  -n, --nrid         display the next available route id
  -i, --rid string   show routing rule matching route ID


```

##### cli route rm

```

    Remove routing rule




```

##### cli route add

```

    Add routing rule

Available Commands:
  a                       Add app/consume routing rule
  b                       Add intermediary forward routing rule
  c                       Add forward routing rule

Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 30s)


```

###### cli route add a

```

    Add app/consume routing rule



Flags:
  -i, --rid string   route id
  -l, --lpk string   local public key
  -m, --lpt string   local port
  -p, --rpk string   remote pk
  -q, --rpt string   remote port

Global Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 30s)


```

###### cli route add b

```

    Add intermediary forward routing rule



Flags:
  -i, --rid string    route id
  -j, --nrid string   next route id
  -k, --tpid string   next transport id

Global Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 30s)


```

###### cli route add c

```

    Add forward routing rule



Flags:
  -i, --rid string    route id
  -j, --nrid string   next route id
  -k, --tpid string   next transport id
  -l, --lpk string    local public key
  -m, --lpt string    local port
  -p, --rpk string    remote pk
  -q, --rpt string    remote port

Global Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 30s)


```

##### cli route find

```
Query the Route Finder
Assumes the local visor public key as an argument if only one argument is given



Flags:
  -n, --min uint16         minimum hops (default 1)
  -x, --max uint16         maximum hops (default 1000)
  -t, --timeout duration   request timeout (default 10s)
  -a, --addr string        route finder service address
                           http://rf.skywire.skycoin.com


```

#### cli tp

```
Display and manage transports of the local visor

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg

Available Commands:
  add                     Add a transport
  rm                      Remove transport(s) by id
  disc                    Discover remote transport(s)
  tree                    tree map of transports on the skywire network

Flags:
  -t, --types strings   show transport(s) type(s) comma-separated
  -p, --pks strings     show transport(s) for public key(s) comma-separated
  -l, --logs            show transport logs (default true)
  -i, --id string       display transport matching ID
  -u, --tptypes         display transport types used by the local visor
      --rpc string      RPC server address (default "localhost:3435")


```

##### cli tp add

```

    Add a transport
		If the transport type is unspecified,
		the visor will attempt to establish a transport
		in the following order: stcpr, sudph, dmsg



Flags:
      --rpc string         RPC server address (default "localhost:3435")
  -r, --rpk string         remote public key.
  -t, --type string        type of transport to add.
  -o, --timeout duration   if specified, sets an operation timeout
  -a, --sdurl string       service discovery url (default "http://sd.skycoin.com")
  -f, --force              attempt transport creation without check of SD
      --cfs string         SD cache file location (default "/tmp/pvisorsd.json")
  -m, --cfa int            update cache files if older than n minutes (default 5)


```

##### cli tp rm

```

    Remove transport(s) by id



Flags:
      --rpc string   RPC server address (default "localhost:3435")
  -a, --all          remove all transports
  -i, --id string    remove transport of given ID


```

##### cli tp disc

```

    Discover remote transport(s) by ID or public key



Flags:
  -i, --id string   obtain transport of given ID
  -p, --pk string   obtain transports by public key


```

##### cli tp tree

```
display a tree representation of transports from TPD

http://tpd.skywire.skycoin.com/all-transports

Set cache file location to "" to avoid using cache files



Flags:
  -a, --tpdurl string   transport discovery url (default "http://tpd.skywire.skycoin.com")
  -w, --uturl string    uptime tracker url (default "http://ut.skywire.skycoin.com")
  -r, --raw             print raw json data
  -p, --pretty          print pretty json data
  -o, --noton           do not filter by online status in UT
      --cft string      TPD cache file location (default "/tmp/tpd.json")
      --cfu string      UT cache file location. (default "/tmp/ut.json")
  -m, --cfa int         update cache files if older than n minutes (default 5)
  -P, --pad int         padding between tree and tpid (default 15)
  -s, --stats           return only statistics


```

#### cli mdisc

```
Query remote DMSG Discovery

Available Commands:
  entry                   Fetch an entry
  servers                 Fetch available servers


```

##### cli mdisc entry

```
Fetch an entry



Flags:
  -a, --addr string   DMSG discovery server address
                      http://dmsgd.skywire.skycoin.com


```

##### cli mdisc servers

```
Fetch available servers



Flags:
      --addr string   address of DMSG discovery server
                       (default "http://dmsgd.skywire.skycoin.com")


```

#### cli completion

```
Generate completion script




```

#### cli log

```
Fetch health, survey, and transport logging from visors which are online in the uptime tracker
http://ut.skywire.skycoin.com/uptimes?v=v2
http://ut.skywire.skycoin.com/uptimes?v=v2&visors=<pk1>;<pk2>;<pk3>

Available Commands:
  st                      survey tree
  tp                      display collected transport bandwidth logging

Flags:
  -e, --env string                deployment to get uptimes from (default "prod")
  -l, --log                       fetch only transport logs
  -v, --survey                    fetch only surveys
  -f, --file string               fetch only a specific file from all online visors
  -k, --pks string                fetch only from specific public keys ; semicolon separated
  -d, --dir string                save files to specified dir (default "log_collecting")
  -c, --clean                     delete files and folders on errors
      --minv string               minimum visor version to fetch from (default "v1.3.19")
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

##### cli log st

```
survey tree



Flags:
  -d, --dir string   path to surveys & transport bandwidth logging
  -p, --pk string    public key to check


```

##### cli log tp

```
display collected transport bandwidth logging



Flags:
  -d, --dir string   path to surveys & transport bandwidth logging


```

#### cli proxy

```
Skysocks client

Available Commands:
  start                   start the proxy client
  stop                    stop the proxy client
  status                  proxy client status
  list                    List servers

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy start

```
start the proxy client



Flags:
  -a, --addr string   address of proxy for use
      --http string   address for http proxy
  -n, --name string   name of skysocks client
  -k, --pk string     server public key
  -t, --timeout int   timeout for starting proxy

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy stop

```
stop the proxy client



Flags:
      --all           stop all skysocks client
      --name string   specific skysocks client that want stop

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy status

```
proxy client status



Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### cli proxy list

```
List proxy servers from service discovery
http://sd.skycoin.com/api/services?type=proxy
http://sd.skycoin.com/api/services?type=proxy&country=US

Set cache file location to "" to avoid using cache files



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




```

#### cli doc

```
generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc > cmd/skywire-cli/README1.md

	generate toc:

	cat cmd/skywire-cli/README1.md | gh-md-toc




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
  se                      skywire environment generator
  sd                      Service discovery server
  nwmon                   Network monitor for skywire VPN and Visor.
  pvm                     Public Visor monitor.
  ssm                     Skysocks monitor.
  vpnm                    VPN monitor.


```

#### svc sn

```

	┌─┐┌─┐┌┬┐┬ ┬┌─┐   ┌┐┌┌─┐┌┬┐┌─┐
	└─┐├┤  │ │ │├─┘───││││ │ ││├┤
	└─┘└─┘ ┴ └─┘┴     ┘└┘└─┘─┴┘└─┘



Flags:
  -m, --metrics string   address to bind metrics API to
  -i, --stdin            read config from STDIN
      --tag string       logging tag (default "setup_node")


```

#### svc tpd

```

	┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐ ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
----- depends: redis, postgresql and initial DB setup -----
sudo -iu postgres createdb tpd
keys-gen | tee tpd-config.json
PG_USER="postgres" PG_DATABASE="tpd" PG_PASSWORD="" transport-discovery --sk $(tail -n1 tpd-config.json)



Flags:
  -a, --addr string             address to bind to[0m (default ":9091")
      --dmsg-disc string        url of dmsg-discovery[0m (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16         dmsg port value
 (default 80)
  -l, --loglvl string           set log level one of: info, error, warn, debug, trace, panic (default "info")
  -m, --metrics string          address to bind metrics API to[0m
      --pg-host string          host of postgres[0m (default "localhost")
      --pg-max-open-conn int    maximum open connection of db (default 60)
      --pg-port string          port of postgres[0m (default "5432")
      --redis string            connections string for a redis store[0m (default "redis://localhost:6379")
      --redis-pool-size int     redis connection pool size[0m (default 10)
      --sk cipher.SecKey        dmsg secret key
 (default 0000000000000000000000000000000000000000000000000000000000000000)
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

Transport setup server for skywire
Takes config in the following format:
{
    "dmsg": {
        "discovery": "http://dmsgd.skywire.skycoin.com",
        "servers": [],
        "sessions_count": 2
    },
    "log_level": "",
    "port":8080,
    "public_key": "",
    "secret_key": "",
    "transport_discovery": "http://tpd.skywire.skycoin.com"
}

Available Commands:
  add                     add transport to remote visor
  rm                      remove transport from remote visor
  list                    list transports of remote visor

Flags:
  -c, --config string   path to config file[0m
  -l, --loglvl string   [info|error|warn|debug|trace|panic] (default "debug")


```

##### svc tps add

```
add transport to remote visor



Flags:
  -1, --from string   PK to request transport setup
  -2, --to string     other transport edge PK
  -t, --type string   transport type to request creation of [stcpr|sudph|dmsg]
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node (default "http://127.0.0.1:8080")


```

##### svc tps rm

```
remove transport from remote visor



Flags:
  -1, --from string   PK to request transport takedown
  -i, --tpid string   id of transport to remove
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node (default "http://127.0.0.1:8080")


```

##### svc tps list

```
list transports of remote visor



Flags:
  -1, --from string   PK to request transport list
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node (default "http://127.0.0.1:8080")


```

#### svc ar

```

	┌─┐┌┬┐┌┬┐┬─┐┌─┐┌─┐┌─┐   ┬─┐┌─┐┌─┐┌─┐┬ ┬  ┬┌─┐┬─┐
	├─┤ ││ ││├┬┘├┤ └─┐└─┐───├┬┘├┤ └─┐│ ││ └┐┌┘├┤ ├┬┘
	┴ ┴─┴┘─┴┘┴└─└─┘└─┘└─┘   ┴└─└─┘└─┘└─┘┴─┘└┘ └─┘┴└─

depends: redis

Note: the specified port must be accessible from the internet ip address or port forwarded for udp
skywire cli config gen-keys > ar-config.json
skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)

Usage:
  skywire svc ar

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
----- depends: postgres and initial db setup -----
sudo -iu postgres createdb rf
skywire cli config gen-keys | tee rf-config.json
PG_USER="postgres" PG_DATABASE="rf" PG_PASSWORD="" route-finder  --addr ":9092" --sk $(tail -n1 rf-config.json)



Flags:
  -a, --addr string            address to bind to[0m (default ":9092")
      --dmsg-disc string       url of dmsg-discovery[0m (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16        dmsg port value
 (default 80)
  -l, --loglvl string          set log level one of: info, error, warn, debug, trace, panic (default "info")
  -m, --metrics string         address to bind metrics API to[0m
      --pg-host string         host of postgres[0m (default "localhost")
      --pg-max-open-conn int   maximum open connection of db (default 60)
      --pg-port string         port of postgres[0m (default "5432")
      --sk cipher.SecKey       dmsg secret key
 (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string             logging tag[0m (default "route_finder")
  -t, --testing                enable testing to start without redis[0m


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
      --tag string       logging tag[0m (default "node-visualizer")
  -t, --testing          enable testing to start without redis[0m


```

#### svc se

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

##### svc se visor

```
Generate config for skywire-visor




```

##### svc se dmsg

```
Generate config for dmsg-server




```

##### svc se setup

```
Generate config for setup node




```

#### svc sd

```

	┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐ ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	└─┐├┤ ├┬┘└┐┌┘││  ├┤───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	└─┘└─┘┴└─ └┘ ┴└─┘└─┘ ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
----- depends: redis, postgresql and initial DB setup -----
sudo -iu postgres createdb sd
keys-gen | tee sd-config.json
PG_USER="postgres" PG_DATABASE="sd" PG_PASSWORD="" service-discovery --sk $(tail -n1 sd-config.json)



Flags:
  -a, --addr string             address to bind to (default ":9098")
  -g, --api-key string          geo API key
  -d, --dmsg-disc string        url of dmsg-discovery (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16         dmsg port value (default 80)
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

#### svc nwmon

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
  socks                   DMSG socks5 proxy server & client
  mon                     DMSG monitor of DMSG discovery entries.


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
----- depends: redis -----
skywire cli config gen-keys > dmsgd-config.json
skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)



Flags:
  -a, --addr string               address to bind to (default ":9090")
      --auth string               auth passphrase as simple auth for official dmsg servers registration
      --dmsgPort uint16           dmsg port value (default 80)
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
skywire dmsg server config gen -o dmsg-config.json
skywire dmsg server start dmsg-config.json

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

#### dmsg socks

```

	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┌─┐┬┌─┌─┐
	 │││││└─┐│ ┬───└─┐│ ││  ├┴┐└─┐
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘└─┘┴ ┴└─┘
DMSG socks5 proxy server & client

Available Commands:
  server                  dmsg socks5 proxy server
  client                  socks5 proxy client for dmsg socks5 proxy server


```

##### dmsg socks server

```
dmsg socks5 proxy server



Flags:
  -D, --dmsg-disc string   dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
  -q, --dport uint16       dmsg port to serve socks5 (default 1081)
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl string          whitelist keys, comma separated


```

##### dmsg socks client

```
socks5 proxy client for dmsg socks5 proxy server



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
  -s, --sleep-deregistration duration   Sleep time for derigstration process in minutes[0m (default 60ns)
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

	UNHIDEFLAGS=1 go run cmd/skywire/skywire.go doc

	UNHIDEFLAGS=1 go run cmd/skywire/skywire.go doc > cmd/skywire/README1.md

	generate toc:

	cat cmd/skywire/README1.md | gh-md-toc




```
