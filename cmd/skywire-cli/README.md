
# skywire-cli documentation

skywire command line interface

* [skywire\-cli documentation](#skywire-cli-documentation)
  * [skywire\-cli](#skywire-cli)
  * [global flags](#global-flags)
  * [subcommand tree](#subcommand-tree)
    * [config](#config)
      * [config gen](#config-gen)
        * [Example for package / msi](#example-for-package--msi)
      * [config gen\-keys](#config-gen-keys)
      * [config check\-pk](#config-check-pk)
      * [config update](#config-update)
        * [config update hv](#config-update-hv)
        * [config update sc](#config-update-sc)
        * [config update ss](#config-update-ss)
        * [config update vpnc](#config-update-vpnc)
        * [config update vpns](#config-update-vpns)
    * [dmsgpty](#dmsgpty)
      * [dmsgpty ui](#dmsgpty-ui)
      * [dmsgpty url](#dmsgpty-url)
      * [dmsgpty list](#dmsgpty-list)
      * [dmsgpty start](#dmsgpty-start)
    * [visor](#visor)
      * [visor app](#visor-app)
        * [visor app ls](#visor-app-ls)
        * [visor app start](#visor-app-start)
        * [visor app stop](#visor-app-stop)
        * [visor app register](#visor-app-register)
        * [visor app deregister](#visor-app-deregister)
        * [visor app log](#visor-app-log)
        * [visor app arg](#visor-app-arg)
          * [visor app arg autostart](#visor-app-arg-autostart)
          * [visor app arg killswitch](#visor-app-arg-killswitch)
          * [visor app arg secure](#visor-app-arg-secure)
          * [visor app arg passcode](#visor-app-arg-passcode)
          * [visor app arg netifc](#visor-app-arg-netifc)
      * [visor hv](#visor-hv)
        * [visor hv ui](#visor-hv-ui)
        * [visor hv cpk](#visor-hv-cpk)
        * [visor hv pk](#visor-hv-pk)
      * [visor pk](#visor-pk)
      * [visor info](#visor-info)
      * [visor ver](#visor-ver)
      * [visor ports](#visor-ports)
      * [visor ip](#visor-ip)
      * [visor ping](#visor-ping)
      * [visor test](#visor-test)
      * [visor start](#visor-start)
      * [visor restart](#visor-restart)
      * [visor reload](#visor-reload)
      * [visor halt](#visor-halt)
      * [visor route](#visor-route)
        * [visor route ls\-rules](#visor-route-ls-rules)
        * [visor route rule](#visor-route-rule)
        * [visor route rm\-rule](#visor-route-rm-rule)
        * [visor route add\-rule](#visor-route-add-rule)
          * [visor route add\-rule app](#visor-route-add-rule-app)
          * [visor route add\-rule fwd](#visor-route-add-rule-fwd)
          * [visor route add\-rule intfwd](#visor-route-add-rule-intfwd)
      * [visor tp](#visor-tp)
        * [visor tp type](#visor-tp-type)
        * [visor tp ls](#visor-tp-ls)
        * [visor tp id](#visor-tp-id)
        * [visor tp add](#visor-tp-add)
        * [visor tp rm](#visor-tp-rm)
        * [visor tp disc](#visor-tp-disc)
    * [vpn](#vpn)
      * [vpn start](#vpn-start)
      * [vpn stop](#vpn-stop)
      * [vpn status](#vpn-status)
      * [vpn list](#vpn-list)
      * [vpn ui](#vpn-ui)
      * [vpn url](#vpn-url)
    * [ut](#ut)
    * [fwd](#fwd)
    * [rev](#rev)
    * [reward](#reward)
    * [survey](#survey)
    * [rtfind](#rtfind)
    * [mdisc](#mdisc)
      * [mdisc entry](#mdisc-entry)
      * [mdisc servers](#mdisc-servers)
    * [completion](#completion)
    * [log](#log)
    * [proxy](#proxy)
      * [proxy start](#proxy-start)
      * [proxy stop](#proxy-stop)
      * [proxy status](#proxy-status)
      * [proxy list](#proxy-list)
    * [tree](#tree)
    * [doc](#doc)




## skywire-cli

```

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴

Usage:
  cli

Available Commands:
  config                  Generate or update a skywire config
  dmsgpty                 Interact with remote visors
  visor                   Query the Skywire Visor
  vpn                     VPN client
  ut                      query uptime tracker
  fwd                     Control skyforwarding
  rev                     reverse proxy skyfwd
  reward                  skycoin reward address
  survey                  system survey
  rtfind                  Query the Route Finder
  mdisc                   Query remote DMSG Discovery
  completion              Generate completion script
  log                     survey & transport log collection
  proxy                   Skysocks client
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

## subcommand tree

A tree representation of the skywire-cli subcommands

```
└─┬cli
  ├─┬config
  │ ├──gen
  │ ├──gen-keys
  │ ├──check-pk
  │ └─┬update
  │   ├──hv
  │   ├──sc
  │   ├──ss
  │   ├──vpnc
  │   └──vpns
  ├─┬dmsgpty
  │ ├──ui
  │ ├──url
  │ ├──list
  │ └──start
  ├─┬visor
  │ ├─┬app
  │ │ ├──ls
  │ │ ├──start
  │ │ ├──stop
  │ │ ├──register
  │ │ ├──deregister
  │ │ ├──log
  │ │ └─┬arg
  │ │   ├──autostart
  │ │   ├──killswitch
  │ │   ├──secure
  │ │   ├──passcode
  │ │   └──netifc
  │ ├─┬hv
  │ │ ├──ui
  │ │ ├──cpk
  │ │ └──pk
  │ ├──pk
  │ ├──info
  │ ├──ver
  │ ├──ports
  │ ├──ip
  │ ├──ping
  │ ├──test
  │ ├──start
  │ ├──restart
  │ ├──reload
  │ ├──halt
  │ ├─┬route
  │ │ ├──ls-rules
  │ │ ├──rule
  │ │ ├──rm-rule
  │ │ └─┬add-rule
  │ │   ├──app
  │ │   ├──fwd
  │ │   └──intfwd
  │ └─┬tp
  │   ├──type
  │   ├──ls
  │   ├──id
  │   ├──add
  │   ├──rm
  │   └──disc
  ├─┬vpn
  │ ├──start
  │ ├──stop
  │ ├──status
  │ ├──list
  │ ├──ui
  │ └──url
  ├──ut
  ├──fwd
  ├──rev
  ├──reward
  ├──survey
  ├──rtfind
  ├─┬mdisc
  │ ├──entry
  │ └──servers
  ├──completion
  ├──log
  ├─┬proxy
  │ ├──start
  │ ├──stop
  │ ├──status
  │ └──list
  ├──tree
  └──doc


```

### config

```
Generate or update the config file used by skywire-visor.

Usage:
  cli config

Available Commands:
  gen                     Generate a config file
  gen-keys                generate public / secret keypair
  check-pk                check a skywire public key
  update                  Update a config file


```

#### config gen

```
Generate a config file

Usage:
  cli config gen [flags]

Flags:
  -a, --url string           services conf url

 (default "http://conf.skywire.skycoin.com")
      --loglvl string        [ debug | warn | error | fatal | panic | trace | info ][0m (default "info")
  -b, --bestproto            best protocol (dmsg | direct) based on location[0m
  -c, --noauth               disable authentication for hypervisor UI[0m
  -d, --dmsghttp             use dmsg connection to skywire services[0m
  -e, --auth                 enable auth on hypervisor UI[0m
  -f, --force                remove pre-existing config[0m
  -g, --disableapps string   comma separated list of apps to disable[0m
  -i, --ishv                 local hypervisor configuration[0m
  -j, --hvpks string         list of public keys to add as hypervisor[0m
      --dmsgpty string       add dmsgpty whitelist PKs
      --survey string        add survey whitelist PKs
      --routesetup string    add route setup node PKs
      --tpsetup string       add transport setup PKs
  -k, --os string            (linux / mac / win) paths[0m (default "linux")
  -l, --publicip             allow display node ip in services[0m
  -m, --example-apps         add example apps to the config[0m
  -n, --stdout               write config to stdout[0m
  -o, --out string           output config[0m
  -p, --pkg                  use path for package: /opt/skywire[0m
  -u, --user                 use paths for user space: /root[0m
  -r, --regen                re-generate existing config & retain keys
  -s, --sk cipher.SecKey     a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --testenv              use test deployment conf.skywire.dev[0m
  -v, --servevpn             enable vpn server[0m
  -w, --hide                 dont print the config to the terminal :: show errors with -n flag[0m
  -x, --retainhv             retain existing hypervisors with regen[0m
  -y, --autoconn             disable autoconnect to public visors[0m
  -z, --public               publicize visor in service discovery[0m
      --stcpr int            set tcp transport listening port - 0 for random[0m
      --sudph int            set udp transport listening port - 0 for random[0m
      --all                  show all flags
      --binpath string       set bin_path[0m
      --nofetch              do not fetch the services from the service conf url
      --nodefaults           do not use hardcoded defaults for production / test services
      --version string       custom version testing override[0m


```

##### Example for package / msi

```
$ skywire-cli config gen -bpirxn --version 1.3.0
{
	"version": "v1.3.7",
	"sk": "794ca4760d823e1a190d3aa19487a276944d54e8c1c8d29e16e6fbe6587eb51e",
	"pk": "02d3879d36c5d8046a81247388af0fd7caef01884c73f9997ddc362ca96d4ff3d3",
	"dmsg": {
		"discovery": "http://dmsgd.skywire.skycoin.com",
		"sessions_count": 1,
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
			"location": "./local/transport_logs",
			"rotation_interval": "168h0m0s"
		},
		"stcpr_port": 0,
		"sudph_port": 0
	},
	"routing": {
		"route_setup_nodes": [
			"0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
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
		"apps": null,
		"server_addr": "localhost:5505",
		"bin_path": "./apps",
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
	"local_path": "./local",
	"dmsghttp_server_path": "./local/custom",
	"stun_servers": [
		"139.162.12.30:3478",
		"170.187.228.181:3478",
		"172.104.161.184:3478",
		"170.187.231.137:3478",
		"143.42.74.91:3478",
		"170.187.225.78:3478",
		"143.42.78.123:3478",
		"139.162.12.244:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null
}
```

#### config gen-keys

```
generate public / secret keypair

Usage:
  cli config gen-keys


```

#### config check-pk

```
check a skywire public key

Usage:
  cli config check-pk <public-key>


```

#### config update

```
Update a config file

Usage:
  cli config update [flags]

Available Commands:
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
  -p, --pkg                      update package config /opt/skywire/skywire.json


```

##### config update hv

```
update hypervisor config

Usage:
  cli config update hv [flags]

Flags:
  -+, --add-pks string   public keys of hypervisors that should be added to this visor
  -r, --reset            resets hypervisor configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -p, --pkg             update package config /opt/skywire/skywire.json


```

##### config update sc

```
update skysocks-client config

Usage:
  cli config update sc [flags]

Flags:
  -+, --add-server string   add skysocks server address to skysock-client
  -r, --reset               reset skysocks-client configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -p, --pkg             update package config /opt/skywire/skywire.json


```

##### config update ss

```
update skysocks-server config

Usage:
  cli config update ss [flags]

Flags:
  -s, --passwd string   add passcode to skysocks server
  -r, --reset           reset skysocks configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -p, --pkg             update package config /opt/skywire/skywire.json


```

##### config update vpnc

```
update vpn-client config

Usage:
  cli config update vpnc [flags]

Flags:
  -x, --killsw string       change killswitch status of vpn-client
      --add-server string   add server address to vpn-client
  -s, --pass string         add passcode of server if needed
  -r, --reset               reset vpn-client configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -p, --pkg             update package config /opt/skywire/skywire.json


```

##### config update vpns

```
update vpn-server config

Usage:
  cli config update vpns [flags]

Flags:
  -s, --passwd string      add passcode to vpn-server
      --secure string      change secure mode status of vpn-server
      --autostart string   change autostart of vpn-server
      --netifc string      set default network interface
  -r, --reset              reset vpn-server configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -p, --pkg             update package config /opt/skywire/skywire.json


```

### dmsgpty

```
Interact with remote visors

Usage:
  cli dmsgpty

Available Commands:
  ui                      Open dmsgpty UI in default browser
  url                     Show dmsgpty UI URL
  list                    List connected visors
  start                   Start dmsgpty session


```

#### dmsgpty ui

```
Open dmsgpty UI in default browser

Usage:
  cli dmsgpty ui [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

#### dmsgpty url

```
Show dmsgpty UI URL

Usage:
  cli dmsgpty url [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

#### dmsgpty list

```
List connected visors

Usage:
  cli dmsgpty list [flags]

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### dmsgpty start

```
Start dmsgpty session

Usage:
  cli dmsgpty start <pk> [flags]

Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")
      --rpc string    RPC server address (default "localhost:3435")


```

### visor

```
Query the Skywire Visor

Usage:
  cli visor [flags]

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
  restart                 restart visor
  halt                    Stop a running visor
  route                   View and set rules
  tp                      View and set transports

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor app

```

  App settings

Usage:
  cli visor app [flags]

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

##### visor app ls

```

  List apps

Usage:
  cli visor app ls [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app start

```

  Launch app

Usage:
  cli visor app start <name> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app stop

```

  Halt app

Usage:
  cli visor app stop <name> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app register

```

  Register app

Usage:
  cli visor app register [flags]

Flags:
  -a, --appname string     name of the app
  -p, --localpath string   path of the local folder (default "./local")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app deregister

```

  Deregister app

Usage:
  cli visor app deregister [flags]

Flags:
  -k, --procKey string   proc key of the app to deregister

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app log

```

  Logs from app since RFC3339Nano-formatted timestamp.


  "beginning" is a special timestamp to fetch all the logs

Usage:
  cli visor app log <name> <timestamp> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app arg

```
App args

Usage:
  cli visor app arg [flags]

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  secure                  Set app secure
  passcode                Set app passcode
  netifc                  Set app network interface

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg autostart

```
Set app autostart

Usage:
  cli visor app arg autostart <name> (true|false) [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg killswitch

```

  Set app killswitch

Usage:
  cli visor app arg killswitch <name> (true|false) [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg secure

```

  Set app secure

Usage:
  cli visor app arg secure <name> (true|false) [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg passcode

```

  Set app passcode.


  "remove" is a special arg to remove the passcode

Usage:
  cli visor app arg passcode <name> <passcode> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg netifc

```
Set app network interface.


  "remove" is a special arg to remove the netifc

Usage:
  cli visor app arg netifc <name> <interface> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor hv

```

  Hypervisor


  Access the hypervisor UI

  View remote hypervisor public key

Usage:
  cli visor hv [flags]

Available Commands:
  ui                      open Hypervisor UI in default browser
  cpk                     Public key of remote hypervisor(s) set in config
  pk                      Public key of remote hypervisor(s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor hv ui

```

  open Hypervisor UI in default browser

Usage:
  cli visor hv ui [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor hv cpk

```

  Public key of remote hypervisor(s) set in config

Usage:
  cli visor hv cpk [flags]

Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from /opt/skywire/skywire.json

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor hv pk

```
Public key of remote hypervisor(s) which are currently connected to

Usage:
  cli visor hv pk [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor pk

```

  Public key of the visor

Usage:
  cli visor pk [flags]

Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from {/opt/skywire/apps /opt/skywire/local {/opt/skywire/users.db %!s(bool=true)}}
  -x, --prt string     serve public key via http (default "7998")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor info

```

  Summary of visor info

Usage:
  cli visor info [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ver

```

  Version and build info

Usage:
  cli visor ver [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ports

```

  List of all ports used by visor services and apps

Usage:
  cli visor ports [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ip

```

  IP information of network

Usage:
  cli visor ip [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ping

```

  Creates a route with the provided pk as a hop and returns latency on the conn

Usage:
  cli visor ping <pk> [flags]

Flags:
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries (default 1)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor test

```

  Creates a route with public visors as a hop and returns latency on the conn

Usage:
  cli visor test [flags]

Flags:
  -c, --count int   Count of Public Visors for using in test. (default 2)
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries per public visors (default 1)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor start

```
start visor

Usage:
  cli visor start [flags]

Flags:
  -s, --src   'go run' external commands from the skywire sources

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor restart

```
restart visor

Usage:
  cli visor restart [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor reload

```
reload visor

Usage:
  cli visor reload [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor halt

```

  Stop a running visor

Usage:
  cli visor halt [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor route

```

    View and set routing rules

Usage:
  cli visor route [flags]

Available Commands:
  ls-rules                List routing rules
  rule                    Return routing rule by route ID key
  rm-rule                 Remove routing rule
  add-rule                Add routing rule

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route ls-rules

```

    List routing rules

Usage:
  cli visor route ls-rules [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route rule

```

    Return routing rule by route ID key

Usage:
  cli visor route rule <route-id> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route rm-rule

```

    Remove routing rule

Usage:
  cli visor route rm-rule <route-id> [flags]

Flags:
  -a, --all   remove all routing rules

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route add-rule

```

    Add routing rule

Usage:
  cli visor route add-rule ( app | fwd | intfwd ) [flags]

Available Commands:
  app                     Add app/consume routing rule
  fwd                     Add forward routing rule
  intfwd                  Add intermediary forward routing rule

Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor route add-rule app

```

    Add app/consume routing rule

Usage:
  cli visor route add-rule app \
               <route-id> \
               <local-pk> \
               <local-port> \
               <remote-pk> \
               <remote-port> \
               ||  [flags]

Flags:
  -i, --rid string   route id
  -l, --lpk string   local public key
  -m, --lpt string   local port
  -p, --rpk string   remote pk
  -q, --rpt string   remote port

Global Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)
      --rpc string            RPC server address (default "localhost:3435")


```

###### visor route add-rule fwd

```

    Add forward routing rule

Usage:
  cli visor route add-rule fwd \
               <route-id> \
               <next-route-id> \
               <next-transport-id> \
               <local-pk> \
               <local-port> \
               <remote-pk> \
               <remote-port> \
               ||  [flags]

Flags:
  -i, --rid string     route id
  -j, --nrid string    next route id
  -k, --ntpid string   next transport id
  -l, --lpk string     local public key
  -m, --lpt string     local port
  -p, --rpk string     remote pk
  -q, --rpt string     remote port

Global Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)
      --rpc string            RPC server address (default "localhost:3435")


```

###### visor route add-rule intfwd

```

    Add intermediary forward routing rule

Usage:
  cli visor route add-rule intfwd \
               <route-id> \
               <next-route-id> \
               <next-transport-id> \
               ||  [flags]

Flags:
  -i, --rid string    route id
  -n, --nrid string   next route id
  -t, --tpid string   next transport id

Global Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)
      --rpc string            RPC server address (default "localhost:3435")


```

#### visor tp

```

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg

Usage:
  cli visor tp [flags]

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

##### visor tp type

```

  Transport types used by the local visor

Usage:
  cli visor tp type

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor tp ls

```

    Available transports

    displays transports of the local visor

Usage:
  cli visor tp ls [flags]

Flags:
  -t, --types strings   show transport(s) type(s) comma-separated
  -p, --pks strings     show transport(s) for public key(s) comma-separated
  -l, --logs            show transport logs (default true)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor tp id

```

    Transport summary by id

Usage:
  cli visor tp id (-i) <transport-id>

Flags:
  -i, --id string   transport ID

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor tp add

```

    Add a transport

    If the transport type is unspecified,
    the visor will attempt to establish a transport
    in the following order: skywire-tcp, stcpr, sudph, dmsg

Usage:
  cli visor tp add (-p) <remote-public-key>

Flags:
  -r, --rpk string         remote public key.
  -o, --timeout duration   if specified, sets an operation timeout
  -t, --type string        type of transport to add.

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor tp rm

```

    Remove transport(s) by id

Usage:
  cli visor tp rm ( -a || -i ) <transport-id>

Flags:
  -a, --all         remove all transports
  -i, --id string   remove transport of given ID

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor tp disc

```

    Discover remote transport(s) by ID or public key

Usage:
  cli visor tp disc (--id=<transport-id> || --pk=<edge-public-key>)

Flags:
  -i, --id string   obtain transport of given ID
  -p, --pk string   obtain transports by public key

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

### vpn

```
VPN client

Usage:
  cli vpn [flags]

Available Commands:
  start                   start the vpn for <public-key>
  stop                    stop the vpnclient
  status                  vpn client status
  list                    List vpn servers
  ui                      Open VPN UI in default browser
  url                     Show VPN UI URL

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn start

```
start the vpn for <public-key>

Usage:
  cli vpn start <public-key> [flags]

Flags:
  -k, --pk string   server public key

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn stop

```
stop the vpnclient

Usage:
  cli vpn stop [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn status

```
vpn client status

Usage:
  cli vpn status [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn list

```
List vpn servers from service discovery
 http://sd.skycoin.com/api/services?type=vpn
 http://sd.skycoin.com/api/services?type=vpn&country=US

Usage:
  cli vpn list [flags]

Flags:
  -c, --country string   filter results by country
  -b, --direct           query service discovery directly
  -n, --num int          number of results to return
  -k, --pk string        check vpn service discovery for public key
  -s, --stats            return only a count of the results
  -u, --unfilter         provide unfiltered results
  -a, --url string       service discovery url default:
                         http://sd.skycoin.com
  -v, --ver string       filter results by version (default "v1.3.7-42-gf9e3cc38")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn ui

```
Open VPN UI in default browser

Usage:
  cli vpn ui [flags]

Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn url

```
Show VPN UI URL

Usage:
  cli vpn url [flags]

Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

### ut

```
query uptime tracker
 Check local visor daily uptime percent with:
 skywire-cli ut -k $(skywire-cli visor pk)

Usage:
  cli ut [flags]

Flags:
  -n, --min int      list visors meeting minimum uptime (default 75)
  -o, --on           list currently online visors
  -k, --pk string    check uptime for the specified key
  -s, --stats        count the number of results
  -u, --url string   specify alternative uptime tracker url
                     default: http://ut.skywire.skycoin.com/uptimes?v=v2


```

### fwd

```
Control skyforwarding
 forward local ports over skywire

Usage:
  cli fwd [flags]

Flags:
  -d, --deregister   deregister local port of the external (http) app
  -l, --ls           list registered local ports
  -p, --port int     local port of the external (http) app


```

### rev

```
connect or disconnect from remote ports

Usage:
  cli rev [flags]

Flags:
  -l, --ls            list configured connections
  -k, --pk string     remote public key to connect to
  -p, --port int      local port to reverse proxy
  -r, --remote int    remote port to read from
  -d, --stop string   disconnect from specified <id>


```

### reward

```

    skycoin reward address set to:

Usage:
  cli reward <address> || [flags]

Flags:
      --all   show all flags


```

### survey

```
print the system survey

Usage:
  cli survey

Flags:
  -s, --sha   generate checksum of system survey


```

```
{
	"timestamp": "2023-05-26T12:39:16.93648714-05:00",
	"public_key": "000000000000000000000000000000000000000000000000000000000000000000",
	"go_os": "linux",
	"go_arch": "amd64",
	"zcalusic_sysinfo": {
		"sysinfo": {
			"version": "0.9.5",
			"timestamp": "2023-05-26T12:39:15.695232998-05:00"
		},
		"node": {
			"hostname": "node",
			"machineid": "0de8bf5b7b2d4637ac44c3de851fb93c",
			"timezone": "America/Chicago"
		},
		"os": {
			"name": "EndeavourOS",
			"vendor": "endeavouros",
			"architecture": "amd64"
		},
		"kernel": {
			"release": "6.3.1-arch2-1",
			"version": "#1 SMP PREEMPT_DYNAMIC Wed, 10 May 2023 08:54:47 +0000",
			"architecture": "x86_64"
		},
		"product": {
			"name": "OptiPlex 7010",
			"vendor": "Dell Inc.",
			"version": "01",
			"serial": "C060HX1"
		},
		"board": {
			"name": "0MN1TX",
			"vendor": "Dell Inc.",
			"version": "A00",
			"serial": "/C060HX1/CN7220035300C5/"
		},
		"chassis": {
			"type": 16,
			"vendor": "Dell Inc.",
			"serial": "C060HX1"
		},
		"bios": {
			"vendor": "Dell Inc.",
			"version": "A25",
			"date": "05/10/2017"
		},
		"cpu": {
			"vendor": "GenuineIntel",
			"model": "Intel(R) Core(TM) i7-3770S CPU @ 3.10GHz",
			"speed": 3100,
			"cache": 8192,
			"cpus": 1,
			"cores": 4,
			"threads": 8
		},
		"memory": {
			"type": "DDR3",
			"speed": 1600,
			"size": 16384
		},
		"storage": [
			{
				"name": "sda",
				"driver": "sd",
				"vendor": "ATA",
				"model": "JAJS600M128C",
				"serial": "30040655357",
				"size": 128
			}
		],
		"network": [
			{
				"name": "eno1",
				"driver": "e1000e",
				"macaddress": "b8:ca:3a:8c:70:23",
				"port": "tp",
				"speed": 1000
			}
		]
	},
	"ip.skycoin.com": {
		"ip_address": "70.121.6.231",
		"latitude": 33.1371,
		"longitude": -96.7488,
		"postal_code": "75035",
		"continent_code": "NA",
		"country_code": "US",
		"country_name": "United States",
		"region_code": "TX",
		"region_name": "Texas",
		"province_code": "",
		"province_name": "",
		"city_name": "Frisco",
		"timezone": "America/Chicago"
	},
	"ip_addr": [
		{
			"ifindex": 1,
			"ifname": "lo",
			"flags": [
				"LOOPBACK",
				"UP",
				"LOWER_UP"
			],
			"mtu": 65536,
			"qdisc": "noqueue",
			"operstate": "UNKNOWN",
			"group": "default",
			"txqlen": 1000,
			"link_type": "loopback",
			"address": "00:00:00:00:00:00",
			"broadcast": "00:00:00:00:00:00",
			"addr_info": [
				{
					"family": "inet",
					"local": "127.0.0.1",
					"prefixlen": 8,
					"scope": "host",
					"label": "lo",
					"valid_life_time": 4294967295,
					"preferred_life_time": 4294967295
				},
				{
					"family": "inet6",
					"local": "::1",
					"prefixlen": 128,
					"scope": "host",
					"valid_life_time": 4294967295,
					"preferred_life_time": 4294967295
				}
			]
		},
		{
			"ifindex": 2,
			"ifname": "eno1",
			"flags": [
				"BROADCAST",
				"MULTICAST",
				"UP",
				"LOWER_UP"
			],
			"mtu": 1500,
			"qdisc": "fq_codel",
			"operstate": "UP",
			"group": "default",
			"txqlen": 1000,
			"link_type": "ether",
			"address": "b8:ca:3a:8c:70:23",
			"broadcast": "ff:ff:ff:ff:ff:ff",
			"addr_info": [
				{
					"family": "inet",
					"local": "192.168.1.57",
					"prefixlen": 24,
					"scope": "global",
					"label": "eno1",
					"valid_life_time": 75286,
					"preferred_life_time": 75286
				},
				{
					"family": "inet6",
					"local": "fe80::419b:25f0:b69a:b34c",
					"prefixlen": 64,
					"scope": "link",
					"valid_life_time": 4294967295,
					"preferred_life_time": 4294967295
				}
			]
		}
	],
	"ghw_blockinfo": {
		"total_size_bytes": 128035676160,
		"disks": [
			{
				"name": "sda",
				"size_bytes": 128035676160,
				"physical_block_size_bytes": 512,
				"drive_type": "ssd",
				"removable": false,
				"storage_controller": "scsi",
				"bus_path": "pci-0000:00:1f.2-ata-1.0",
				"vendor": "ATA",
				"model": "JAJS600M128C",
				"serial_number": "30040655357",
				"wwn": "0x5000000000003244",
				"partitions": [
					{
						"name": "sda1",
						"label": "unknown",
						"mount_point": "/",
						"size_bytes": 128033659904,
						"type": "ext4",
						"read_only": false,
						"uuid": "514fad51-01",
						"filesystem_label": "unknown"
					}
				]
			}
		]
	},
	"ghw_productinfo": {
		"family": "",
		"name": "OptiPlex 7010",
		"vendor": "Dell Inc.",
		"serial_number": "C060HX1",
		"uuid": "4c4c4544-0030-3610-8030-c3c04f485831",
		"sku": "OptiPlex 7010",
		"version": "01"
	},
	"ghw_memoryinfo": {
		"total_physical_bytes": 17179869184,
		"total_usable_bytes": 16655327232,
		"supported_page_sizes": [
			2097152
		],
		"modules": null
	},
	"uuid": "99246216-8786-4332-a8e2-b1bb15e68574",
	"skywire_version": "fatal: detected dubious ownership in repository at '/home/d0mo/go/src/github.com/0pcom/skywire'\nTo add an exception for this directory, call:\n\n\tgit config --global --add safe.directory /home/d0mo/go/src/github.com/0pcom/skywire\n"
}
```

### rtfind

```
Query the Route Finder
Assumes the local visor public key as an argument if only one argument is given

Usage:
  cli rtfind <public-key> | <public-key-visor-1> <public-key-visor-2> [flags]

Flags:
  -n, --min uint16         minimum hops (default 1)
  -x, --max uint16         maximum hops (default 1000)
  -t, --timeout duration   request timeout (default 10s)
  -a, --addr string        route finder service address
                           http://rf.skywire.skycoin.com


```

### mdisc

```
Query remote DMSG Discovery

Usage:
  cli mdisc

Available Commands:
  entry                   Fetch an entry
  servers                 Fetch available servers


```

#### mdisc entry

```
Fetch an entry

Usage:
  cli mdisc entry <visor-public-key> [flags]

Flags:
  -a, --addr string   DMSG discovery server address
                      http://dmsgd.skywire.skycoin.com


```

#### mdisc servers

```
Fetch available servers

Usage:
  cli mdisc servers [flags]

Flags:
      --addr string   address of DMSG discovery server
                       (default "http://dmsgd.skywire.skycoin.com")


```

### completion

```
Generate completion script

Usage:
  cli completion [bash|zsh|fish|powershell]


```

### log

```
collect surveys and transport logging from visors which are online in the uptime tracker

Usage:
  cli log [flags]

Flags:
  -e, --env string         selecting env to fetch uptimes, default is prod (default "prod")
  -l, --log                fetch only transport logs
  -v, --survey             fetch only surveys
  -c, --clean              delete files and folders on errors
      --minv string        minimum version for get logs, default is 1.3.4 (default "v1.3.4")
  -n, --duration int       numberof days before today to fetch transport logs for (default 1)
      --all                consider all visors ; no version filtering
      --batchSize int      number of visor in each batch, default is 50 (default 50)
      --maxfilesize int    maximum file size allowed to download during collecting logs, in KB (default 30)
  -D, --dmsg-disc string   dmsg discovery url
                            (default "http://dmsgd.skywire.skycoin.com")
  -u, --ut string          custom uptime tracker url
  -s, --sk cipher.SecKey   a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)


```

### proxy

```
Skysocks client

Usage:
  cli proxy [flags]

Available Commands:
  start                   start the proxy client
  stop                    stop the proxy client
  status                  proxy client status
  list                    List servers

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### proxy start

```
start the proxy client

Usage:
  cli proxy start [flags]

Flags:
  -k, --pk string   server public key

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### proxy stop

```
stop the proxy client

Usage:
  cli proxy stop [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### proxy status

```
proxy client status

Usage:
  cli proxy status [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### proxy list

```
List proxy servers from service discovery
 http://sd.skycoin.com/api/services?type=proxy
 http://sd.skycoin.com/api/services?type=proxy&country=US

Usage:
  cli proxy list [flags]

Flags:
  -c, --country string   filter results by country
  -b, --direct           query service discovery directly
  -n, --num int          number of results to return (0 = all)
  -k, --pk string        check proxy service discovery for public key
  -s, --stats            return only a count of the results
  -u, --unfilter         provide unfiltered results
  -a, --url string       service discovery url default:
                         http://sd.skycoin.com
  -v, --ver string       filter results by version (default "v1.3.7-42-gf9e3cc38")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

### tree

```
subcommand tree

Usage:
  cli tree


```

### doc

```
generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc > cmd/skywire-cli/README1.md

	generate toc:

	cat cmd/skywire-cli/README1.md | gh-md-toc

Usage:
  cli doc


```
