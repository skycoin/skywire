
# skywire-cli documentation

skywire command line interface

  * [skywire\-cli](#skywire-cli)
  * [global flags](#global-flags)
  * [subcommand tree](#subcommand-tree)
    * [config](#config)
      * [config gen](#config-gen)
        * [Example for package / msi](#example-for-package--msi)
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
      * [visor exec](#visor-exec)
      * [visor hv](#visor-hv)
        * [visor hv ui](#visor-hv-ui)
        * [visor hv cpk](#visor-hv-cpk)
        * [visor hv pk](#visor-hv-pk)
      * [visor pk](#visor-pk)
      * [visor info](#visor-info)
      * [visor ver](#visor-ver)
      * [visor ports](#visor-ports)
      * [visor ping](#visor-ping)
      * [visor test](#visor-test)
      * [visor route](#visor-route)
        * [visor route ls\-rules](#visor-route-ls-rules)
        * [visor route rule](#visor-route-rule)
        * [visor route rm\-rule](#visor-route-rm-rule)
        * [visor route add\-rule](#visor-route-add-rule)
          * [visor route add\-rule app](#visor-route-add-rule-app)
          * [visor route add\-rule fwd](#visor-route-add-rule-fwd)
          * [visor route add\-rule intfwd](#visor-route-add-rule-intfwd)
      * [visor halt](#visor-halt)
      * [visor start](#visor-start)
      * [visor tp](#visor-tp)
        * [visor tp type](#visor-tp-type)
        * [visor tp ls](#visor-tp-ls)
        * [visor tp id](#visor-tp-id)
        * [visor tp add](#visor-tp-add)
        * [visor tp rm](#visor-tp-rm)
        * [visor tp disc](#visor-tp-disc)
    * [vpn](#vpn)
      * [vpn list](#vpn-list)
      * [vpn ui](#vpn-ui)
      * [vpn url](#vpn-url)
      * [vpn start](#vpn-start)
      * [vpn stop](#vpn-stop)
      * [vpn status](#vpn-status)
    * [fwd](#fwd)
    * [rev](#rev)
    * [reward](#reward)
    * [survey](#survey)
    * [rtfind](#rtfind)
    * [mdisc](#mdisc)
      * [mdisc entry](#mdisc-entry)
      * [mdisc servers](#mdisc-servers)
    * [completion](#completion)
    * [tree](#tree)
    * [doc](#doc)



## skywire-cli

```

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴

Usage:
  skywire-cli

Available Commands:
  config                  Generate or update a skywire config
  dmsgpty                 Interact with remote visors
  visor                   Query the Skywire Visor
  vpn                     controls for VPN client
  fwd                     Control skyforwarding
  rev                     reverse proxy skyfwd
  reward                  skycoin reward address
  survey                  system survey
  rtfind                  Query the Route Finder
  mdisc                   Query remote DMSG Discovery
  completion              Generate completion script
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
└─┬skywire-cli
  ├─┬config
  │ ├──gen
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
  │ ├──exec
  │ ├─┬hv
  │ │ ├──ui
  │ │ ├──cpk
  │ │ └──pk
  │ ├──pk
  │ ├──info
  │ ├──ver
  │ ├──ports
  │ ├──ping
  │ ├──test
  │ ├─┬route
  │ │ ├──ls-rules
  │ │ ├──rule
  │ │ ├──rm-rule
  │ │ └─┬add-rule
  │ │   ├──app
  │ │   ├──fwd
  │ │   └──intfwd
  │ ├──halt
  │ ├──start
  │ └─┬tp
  │   ├──type
  │   ├──ls
  │   ├──id
  │   ├──add
  │   ├──rm
  │   └──disc
  ├─┬vpn
  │ ├──list
  │ ├──ui
  │ ├──url
  │ ├──start
  │ ├──stop
  │ └──status
  ├──fwd
  ├──rev
  ├──reward
  ├──survey
  ├──rtfind
  ├─┬mdisc
  │ ├──entry
  │ └──servers
  ├──completion
  ├──tree
  ├──doc
  └──

```

### config

```
A primary function of skywire-cli is generating and updating the config file used by skywire-visor.

Usage:
  skywire-cli config

Available Commands:
  gen                     Generate a config file
  update                  Update a config file


```

#### config gen

```
Generate a config file

Usage:
  skywire-cli config gen [flags]

Flags:
  -a, --url string           services conf
      --log-level string     level of logging in config (default "info")
  -b, --bestproto            best protocol (dmsg | direct) based on location
  -c, --noauth               disable authentication for hypervisor UI
  -d, --dmsghttp             use dmsg connection to skywire services
  -e, --auth                 enable auth on hypervisor UI
  -f, --force                remove pre-existing config
  -g, --disableapps string   comma separated list of apps to disable
  -i, --ishv                 local hypervisor configuration
  -j, --hvpks string         list of public keys to use as hypervisor
  -k, --os string            (linux / mac / win) paths (default "linux")
  -l, --publicip             allow display node ip in services
  -m, --example-apps         add example apps to the config
  -n, --stdout               write config to stdout
  -o, --out string           output config: skywire-config.json
  -p, --pkg                  use path for package: /opt/skywire
  -q, --publicrpc            allow rpc requests from LAN
  -r, --regen                re-generate existing config & retain keys
  -s, --sk cipher.SecKey     a random key is generated if unspecified

 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --testenv              use test deployment conf.skywire.dev
  -u, --user                 use paths for user space: /home/d0mo
  -v, --servevpn             enable vpn server
  -w, --hide                 dont print the config to the terminal
  -x, --retainhv             retain existing hypervisors with regen
  -y, --autoconn             disable autoconnect to public visors
  -z, --public               publicize visor in service discovery
      --version string       custom version testing override
      --all                  show all flags
      --binpath string       set bin_path


```

##### Example for package / msi

```
$ skywire-cli config gen -bpirxn --version 1.3.0
{
	"version": "fatal: No names found, cannot describe anything.\n",
	"sk": "8111629add1de63c18f43dc052a76a4cdc948c978bf7847bd2ccb8e61f30a7f7",
	"pk": "03643894e84e55e7d7915b7f971b090e582261609c9188066983c70c5d92a0b4ae",
	"dmsg": {
		"discovery": "http://dmsgd.skywire.skycoin.com",
		"sessions_count": 1,
		"servers": []
	},
	"dmsgpty": {
		"dmsg_port": 22,
		"cli_network": "unix",
		"cli_address": "/tmp/dmsgpty.sock"
	},
	"skywire-tcp": {
		"pk_table": null,
		"listening_address": ":7777"
	},
	"transport": {
		"discovery": "http://tpd.skywire.skycoin.com",
		"address_resolver": "http://ar.skywire.skycoin.com",
		"public_autoconnect": true,
		"transport_setup_nodes": null,
		"log_store": {
			"type": "file",
			"location": "./local/transport_logs",
			"rotation_interval": "168h0m0s"
		}
	},
	"routing": {
		"setup_nodes": [
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
		"bin_path": "./build/apps",
		"display_node_ip": false
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"local_path": "./local",
	"custom_dmsg_http_path": "./local/custom",
	"stun_servers": [
		"192.53.116.178:3478",
		"172.105.114.227:3478",
		"172.104.47.121:3478",
		"172.104.185.252:3478",
		"139.162.42.104:3478",
		"192.53.172.10:3478",
		"172.104.54.73:3478",
		"139.162.21.168:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null
}
```

#### config update

```
Update a config file

Usage:
  skywire-cli config update [flags]

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


```

##### config update hv

```
update hypervisor config

Usage:
  skywire-cli config update hv [flags]

Flags:
  -+, --add-pks string   public keys of hypervisors that should be added to this visor
  -r, --reset            resets hypervisor configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output


```

##### config update sc

```
update skysocks-client config

Usage:
  skywire-cli config update sc [flags]

Flags:
  -+, --add-server string   add skysocks server address to skysock-client
  -r, --reset               reset skysocks-client configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output


```

##### config update ss

```
update skysocks-server config

Usage:
  skywire-cli config update ss [flags]

Flags:
  -s, --passwd string   add passcode to skysocks server
  -r, --reset           reset skysocks configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output


```

##### config update vpnc

```
update vpn-client config

Usage:
  skywire-cli config update vpnc [flags]

Flags:
  -x, --killsw string       change killswitch status of vpn-client
      --add-server string   add server address to vpn-client
  -s, --pass string         add passcode of server if needed
  -r, --reset               reset vpn-client configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output


```

##### config update vpns

```
update vpn-server config

Usage:
  skywire-cli config update vpns [flags]

Flags:
  -s, --passwd string      add passcode to vpn-server
      --secure string      change secure mode status of vpn-server
      --autostart string   change autostart of vpn-server
      --netifc string      set default network interface
  -r, --reset              reset vpn-server configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output


```

### dmsgpty

```
Interact with remote visors

Usage:
  skywire-cli dmsgpty

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
  skywire-cli dmsgpty ui [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

#### dmsgpty url

```
Show dmsgpty UI URL

Usage:
  skywire-cli dmsgpty url [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to


```

#### dmsgpty list

```
List connected visors

Usage:
  skywire-cli dmsgpty list [flags]

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### dmsgpty start

```
Start dmsgpty session

Usage:
  skywire-cli dmsgpty start <pk> [flags]

Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")
      --rpc string    RPC server address (default "localhost:3435")


```

### visor

```
Query the Skywire Visor

Usage:
  skywire-cli visor [flags]

Available Commands:
  app                     App settings
  exec                    Execute a command
  hv                      Hypervisor
  pk                      Public key of the visor
  info                    Summary of visor info
  ver                     Version and build info
  ports                   List of Ports
  ping                    Ping the visor with given pk
  test                    Test the visor with public visors on network
  route                   View and set rules
  halt                    Stop a running visor
  start                   Start a visor
  tp                      View and set transports

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor app

```

  App settings

Usage:
  skywire-cli visor app [flags]

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
  skywire-cli visor app ls [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app start

```

  Launch app

Usage:
  skywire-cli visor app start <name> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app stop

```

  Halt app

Usage:
  skywire-cli visor app stop <name> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app register

```

  Register app

Usage:
  skywire-cli visor app register [flags]

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
  skywire-cli visor app deregister [flags]

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
  skywire-cli visor app log <name> <timestamp> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor app arg

```
App args

Usage:
  skywire-cli visor app arg [flags]

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
  skywire-cli visor app arg autostart <name> (true|false) [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg killswitch

```

  Set app killswitch

Usage:
  skywire-cli visor app arg killswitch <name> (true|false) [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg secure

```

  Set app secure

Usage:
  skywire-cli visor app arg secure <name> (true|false) [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg passcode

```

  Set app passcode.


  "remove" is a special arg to remove the passcode

Usage:
  skywire-cli visor app arg passcode <name> <passcode> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

###### visor app arg netifc

```
Set app network interface.


  "remove" is a special arg to remove the netifc

Usage:
  skywire-cli visor app arg netifc <name> <interface> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor exec

```

  Execute a command

Usage:
  skywire-cli visor exec <command> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor hv

```

  Hypervisor


  Access the hypervisor UI

  View remote hypervisor public key

Usage:
  skywire-cli visor hv [flags]

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
  skywire-cli visor hv ui [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor hv cpk

```

  Public key of remote hypervisor(s) set in config

Usage:
  skywire-cli visor hv cpk [flags]

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
  skywire-cli visor hv pk [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor pk

```

  Public key of the visor

Usage:
  skywire-cli visor pk [flags]

Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from /opt/skywire/skywire.json
  -x, --prt string     serve public key via http (default "7998")

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor info

```

  Summary of visor info

Usage:
  skywire-cli visor info [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ver

```

  Version and build info

Usage:
  skywire-cli visor ver [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ports

```

  List of all ports used by visor services and apps

Usage:
  skywire-cli visor ports [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor ping

```

	Creates a route with the provided pk as a hop and returns latency on the conn

Usage:
  skywire-cli visor ping <pk> [flags]

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
  skywire-cli visor test [flags]

Flags:
  -c, --count int   Count of Public Visors for using in test. (default 2)
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries per public visors (default 1)

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor route

```

    View and set routing rules

Usage:
  skywire-cli visor route [flags]

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
  skywire-cli visor route ls-rules [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route rule

```

    Return routing rule by route ID key

Usage:
  skywire-cli visor route rule <route-id> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route rm-rule

```

    Remove routing rule

Usage:
  skywire-cli visor route rm-rule <route-id> [flags]

Flags:
  -a, --all   remove all routing rules

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor route add-rule

```

    Add routing rule

Usage:
  skywire-cli visor route add-rule ( app | fwd | intfwd ) [flags]

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
  skywire-cli visor route add-rule app \
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
  skywire-cli visor route add-rule fwd \
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
  skywire-cli visor route add-rule intfwd \
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

#### visor halt

```

  Stop a running visor

Usage:
  skywire-cli visor halt [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor start

```

  Start a visor

Usage:
  skywire-cli visor start [flags]

Flags:
  -s, --src   'go run' external commands from the skywire sources

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### visor tp

```

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

Usage:
  skywire-cli visor tp [flags]

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
  skywire-cli visor tp type

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

##### visor tp ls

```

    Available transports

    displays transports of the local visor

Usage:
  skywire-cli visor tp ls [flags]

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
  skywire-cli visor tp id (-i) <transport-id>

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
  skywire-cli visor tp add (-p) <remote-public-key>

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
  skywire-cli visor tp rm ( -a || -i ) <transport-id>

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
  skywire-cli visor tp disc (--id=<transport-id> || --pk=<edge-public-key>)

Flags:
  -i, --id string   obtain transport of given ID
  -p, --pk string   obtain transports by public key

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

### vpn

```
controls for VPN client

Usage:
  skywire-cli vpn [flags]

Available Commands:
  list                    List public VPN servers
  ui                      Open VPN UI in default browser
  url                     Show VPN UI URL
  start                   start the vpn for <public-key>
  stop                    stop the vpn
  status                  vpn status

Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn list

```
List public VPN servers

Usage:
  skywire-cli vpn list [flags]

Flags:
  -c, --country string   filter results by country
  -n, --nofilter         provide unfiltered results
  -s, --stats            return only a count of the results
  -v, --ver string       filter results by version

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn ui

```
Open VPN UI in default browser

Usage:
  skywire-cli vpn ui [flags]

Flags:
  -c, --config string   config path
  -p, --pkg             use package config path

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn url

```
Show VPN UI URL

Usage:
  skywire-cli vpn url [flags]

Flags:
  -c, --config string   config path
  -p, --pkg             use package config path

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn start

```
start the vpn for <public-key>

Usage:
  skywire-cli vpn start <public-key> [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn stop

```
stop the vpn

Usage:
  skywire-cli vpn stop [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

#### vpn status

```
vpn status

Usage:
  skywire-cli vpn status [flags]

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")


```

### fwd

```
Control skyforwarding
 forward local ports over skywire

Usage:
  skywire-cli fwd [flags]

Flags:
  -d, --deregister   deregister local port of the external (http) app
  -l, --ls           list registered local ports
  -p, --port int     local port of the external (http) app


```

### rev

```
connect or disconnect from remote ports

Usage:
  skywire-cli rev [flags]

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
    2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6

Usage:
  skywire-cli reward <address> || [flags]

Flags:
      --all   show all flags


```

### survey

```
print the system survey

Usage:
  skywire-cli survey

Flags:
  -s, --sha   generate checksum of system survey


```

```
{
	"public_key": "000000000000000000000000000000000000000000000000000000000000000000",
	"skycoin_address": "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6",
	"go_os": "linux",
	"go_arch": "amd64",
	"zcalusic_sysinfo": {
		"sysinfo": {
			"version": "0.9.5",
			"timestamp": "2023-01-12T09:49:16.860941383-06:00"
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
			"release": "6.1.1-arch1-1",
			"version": "#1 SMP PREEMPT_DYNAMIC Wed, 21 Dec 2022 22:27:55 +0000",
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
					"valid_life_time": 81925,
					"preferred_life_time": 81925
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
						"uuid": "514fad51-01"
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
		"total_usable_bytes": 16657915904,
		"supported_page_sizes": [
			2097152
		],
		"modules": null
	},
	"uuid": "69573fd2-59c9-4cb2-ba77-7ad93f1b4bfa",
	"skywire_version": "fatal: No names found, cannot describe anything.\n"
}
```

### rtfind

```
Query the Route Finder

Usage:
  skywire-cli rtfind <public-key-visor-1> <public-key-visor-2> [flags]

Flags:
  -n, --min-hops uint16    minimum hops (default 1)
  -x, --max-hops uint16    maximum hops (default 1000)
  -t, --timeout duration   request timeout (default 10s)
  -a, --addr string        route finder service address
                            (default "http://rf.skywire.skycoin.com")


```

### mdisc

```
Query remote DMSG Discovery

Usage:
  skywire-cli mdisc

Available Commands:
  entry                   Fetch an entry
  servers                 Fetch available servers


```

#### mdisc entry

```
Fetch an entry

Usage:
  skywire-cli mdisc entry <visor-public-key> [flags]

Flags:
      --addr string   address of DMSG discovery server
                       (default "http://dmsgd.skywire.skycoin.com")


```

#### mdisc servers

```
Fetch available servers

Usage:
  skywire-cli mdisc servers [flags]

Flags:
      --addr string   address of DMSG discovery server
                       (default "http://dmsgd.skywire.skycoin.com")


```

### completion

```
Generate completion script

Usage:
  skywire-cli completion [bash|zsh|fish|powershell]


```

### tree

```
subcommand tree

Usage:
  skywire-cli tree


```

### doc

```
generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-cli/skywire-cli.go doc > cmd/skywire-cli/README1.md

	generate toc:

	cat cmd/skywire-cli/README1.md | gh-md-toc

Usage:
  skywire-cli doc


```

###

```

```
