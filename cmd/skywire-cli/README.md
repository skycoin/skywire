# CLI Documentation

skywire command line interface

<!-- MarkdownTOC autolink="true" bracket="round" levels="1,2,3" -->

- [Install](#install)
- [skywire-cli usage](#skywire-cli-usage)
	- [mdisc usage](#mdisc-usage)
    - [available servers](#available-servers)
		- [entry](#entry)
	- [visor usage](#visor-usage)
    - [add rule](#add-rule)
		- [add tp](#add-tp)
		- [app logs since](#app-logs-since)
		- [disc tp](#disc-tp)
		- [exec](#exec)
		- [gen config](#gen-config)
		- [ls apps](#ls-apps)
		- [ls rules](#ls-rules)
		- [ls tp](#ls-tp)
		- [ls types](#ls-types)
		- [pk](#pk)
		- [rm rule](#rm-rule)
		- [rm tp](#rm-tp)
		- [rule](#rule)
		- [set app autostart](#set-app-autostart)
		- [start app](#start-app)
		- [stop app](#stop-app)
		- [tp](#tp)
		- [update config](#update-config)
		- [version](#version)
  - [rtfind usage](#rtfind-usage)
<!-- /MarkdownTOC -->

## Install

```bash
$ cd $GOPATH/src/github.com/skycoin/skywire/cmd/skywire-cli
$ go install ./...
```

## skywire-cli usage

After the installation, you can run `skywire-cli` to see the usage:

```
$ skywire-cli
Command Line Interface for skywire

Usage:
  skywire-cli [command]

Available Commands:
  help        Help about any command
  mdisc       Contains sub-commands that interact with a remote DMSG Discovery
  rtfind      Queries the Route Finder for available routes between two visors
  visor       Contains sub-commands that interact with the local Skywire Visor

Flags:
  -h, --help   help for skywire-cli

Use "skywire-cli [command] --help" for more information about a command.
```

### mdisc usage

```
$ skywire-cli mdisc
Contains sub-commands that interact with a remote DMSG Discovery

Usage:
  skywire-cli mdisc [command]

Available Commands:
  available-servers fetch available servers from DMSG discovery
  entry             fetches an entry from DMSG discovery

Flags:
      --addr string   address of DMSG discovery server (default "http://dmsg.discovery.skywire.skycoin.com")
  -h, --help          help for mdisc

Use "skywire-cli mdisc [command] --help" for more information about a command.
```

#### available servers

```
$ skywire-cli mdisc available-servers
```

```
Flags:
      --addr string   address of DMSG discovery server
```

##### Example

```
$ skywire-cli mdisc available-servers
[2021-06-23T12:39:14-05:00] DEBUG disc.NewHTTP [disc]: Created HTTP client. addr="http://dmsg.discovery.skywire.skycoin.com"
version     registered              public-key                                                             address                                           available-sessions
0.0.1       1624470017599202949     02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0     dmsg.server02a4.skywire.skycoin.com:30080         2056
0.0.1       1624470011287159507     03d5b55d1133b26485c664cf8b95cff6746d1e321c34e48c9fed293eff0d6d49e5     dmsg.server03d5b55d.skywire.skycoin.com:30083     2056
0.0.1       1624470011335956138     03717576ada5b1744e395c66c2bb11cea73b0e23d0dcd54422139b1a7f12e962c4     dmsg.server03717576.skywire.skycoin.com:30082     2056
0.0.1       1624470023350445564     02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd     dmsg.server02a2d4c3.skywire.skycoin.com:30081     2056

```

#### entry

```
$ skywire-cli mdisc entry <visor-public-key>
```

```
Flags:
      --addr string   address of DMSG discovery server
```

##### Example

```
$ skywire-cli mdisc entry 03a5a12feb32e26fb73b85639c7e6b54f119c71ff86a6607e5f22c6f8852c8909e
[2021-06-23T12:48:18-05:00] DEBUG disc.NewHTTP [disc]: Created HTTP client. addr="http://dmsg.discovery.skywire.skycoin.com"
	version: 0.0.1
	sequence: 10820
	registered at: 1624470556659947194
	static public key: 03a5a12feb32e26fb73b85639c7e6b54f119c71ff86a6607e5f22c6f8852c8909e
	signature: 5b6b8a233a812b27d3d6f9d62285cf3302eb7e31f5702107cf3edb20504da3e07e0501c63245b24ca0acc7617751ac39cbbca73507b036331a03ea090fd8383601
	entry is registered as client. Related info:
		delegated servers:
			02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd
			03d5b55d1133b26485c664cf8b95cff6746d1e321c34e48c9fed293eff0d6d49e5
			03717576ada5b1744e395c66c2bb11cea73b0e23d0dcd54422139b1a7f12e962c4

```

### visor usage

```
$ skywire-cli visor -h

Contains sub-commands that interact with the local Skywire Visor

Usage:
  skywire-cli visor [command]

Available Commands:
  add-rule          Adds a new routing rule
  add-tp            Adds a new transport
  app-logs-since    Gets logs from given app since RFC3339Nano-formated timestamp. "beginning" is a special timestamp to fetch all the logs
  disc-tp           Queries the Transport Discovery to find transport(s) of given transport ID or edge public key
  exec              Executes the given command
  gen-config        Generates a config file
  ls-apps           Lists apps running on the local visor
  ls-rules          Lists the local visor's routing rules
  ls-tp             Lists the available transports with optional filter flags
  ls-types          Lists transport types used by the local visor
  pk                Obtains the public key of the visor
  rm-rule           Removes a routing rule via route ID key
  rm-tp             Removes transport with given id
  rule              Returns a routing rule via route ID key
  set-app-autostart Sets the autostart flag for an app of given name
  start-app         Starts an app of given name
  stop-app          Stops an app of given name
  tp                Returns summary of given transport by id
  update-config     Updates a config file
  version           Obtains version and build info of the node

Flags:
  -h, --help         help for visor
      --rpc string   RPC server address (default "localhost:3435")

Use "skywire-cli visor [command] --help" for more information about a command.
```

#### add rule

add rule

```
$ skywire-cli visor add-rule (app <route-id> <local-pk> <local-port> <remote-pk> <remote-port> | fwd <next-route-id> <next-transport-id>) [flags]
```

##### Example

```
$ $ skywire-cli visor add-rule -h
Adds a new routing rule

Usage:
  skywire-cli visor add-rule (app <route-id> <local-pk> <local-port> <remote-pk> <remote-port> | fwd <next-route-id> <next-transport-id>) [flags]

Flags:
  -h, --help                  help for add-rule
      --keep-alive duration   duration after which routing rule will expire if no activity is present (default 30s)

```


#### add tp

add transport

```
$ skywire-cli visor add-tp <remote-public-key> [flags]
```

##### Example

```
$ $ skywire-cli visor add-tp -h
Adds a new transport

Usage:
  skywire-cli visor add-tp <remote-public-key> [flags]

Flags:
  -h, --help               help for add-tp
      --public             whether to make the transport public (default true)
  -t, --timeout duration   if specified, sets an operation timeout
      --type string        type of transport to add; if unspecified, cli will attempt to establish a transport in the following order: stcp, stcpr, sudph, dmsg
```

#### app logs since

application logs since

```
$ skywire-cli visor app-logs-since <name> <timestamp>
```

##### Example

```
$ skywire-cli visor app-logs-since skysocks beginning                           
[[2021-06-01T16:34:56Z] INFO (STDOUT) [proc:skysocks:101c6bdecf1f4b138a1f1b55b124c2a4]: Starting serving proxy server                                                                                   
 [2021-06-01T17:17:36Z] INFO (STDOUT) [proc:skysocks:282ff61851e44ea98277ae29694b1eba]: Version "0.4.1" built on "2021-03-19T23:26:21Z" against commit "d804a8ce"                                       
 [2021-06-01T17:17:37Z] INFO (STDOUT) [proc:skysocks:282ff61851e44ea98277ae29694b1eba]: Starting serving proxy server                                                                                   
 [2021-06-01T18:17:34Z] INFO (STDOUT) [proc:skysocks:e30aac10262f4c269a8554b1f64dcb01]: Starting serving proxy server                                                                                   
 [2021-06-01T18:17:36Z] INFO (STDOUT) [proc:skysocks:fca4f48650514e6bb8c9f7aadfaacdfc]: Starting serving proxy server                                                                                   
 [2021-06-01T18:17:38Z] INFO (STDOUT) [proc:skysocks:f96f05677adb48adac64370a96dadb48]: Starting serving proxy server                                                                                   
 [2021-06-01T18:17:39Z] INFO (STDOUT) [proc:skysocks:fb4d4f9f338441c793ecf7017b99c948]: Starting serving proxy server                                                                                   
 [2021-06-06T14:17:17Z] INFO (STDOUT) [proc:skysocks:1dcfc8241871421aaad797349a3ba1c1]: Version "0.4.1" built on "2021-03-19T23:26:21Z" against commit "d804a8ce"                                       
 [2021-06-06T14:17:18Z] INFO (STDOUT) [proc:skysocks:1dcfc8241871421aaad797349a3ba1c1]: Starting serving proxy server                                                                                   
]                                                       
```

#### disc tp

discover transports or transport discovery

```
$ skywire-cli visor disc-tp (--id=<transport-id> | --pk=<edge-public-key>)
```

##### Example

```
$ $ skywire-cli visor disc-tp -h
Queries the Transport Discovery to find transport(s) of given transport ID or edge public key

Usage:
  skywire-cli visor disc-tp (--id=<transport-id> | --pk=<edge-public-key>) [flags]

Flags:
  -h, --help               help for disc-tp
      --id transportID     if specified, obtains a single transport of given ID (default 00000000-0000-0000-0000-000000000000)
      --pk cipher.PubKey   if specified, obtains transports associated with given public key (default 000000000000000000000000000000000000000000000000000000000000000000)

```

#### exec

execute a given command

```
$ skywire-cli visor exec <command>
```

##### Example

ls
```
$ skywire-cli visor exec ls
bin
boot
dev
efi
etc
home
lib
lib64
media
mnt
opt
proc
root
run
sbin
share
srv
sys
tmp
usr
var
```

echo

```
$ skywire-cli visor exec echo "hello world"
hello world
```


#### gen config

```
$ skywire-cli visor gen-config --help
Generates a config file

Usage:
  skywire-cli visor gen-config [flags]

Flags:
  -h, --help                    help for gen-config
      --hypervisor-pks string   public keys of hypervisors that should be added to this visor
      --is-hypervisor           whether to generate config to run this visor as a hypervisor.
  -o, --output string           path of output config file. (default "skywire-config.json")
  -p, --package                 use defaults for package-based installations
  -r, --replace                 whether to allow rewrite of a file that already exists (this retains the keys).
      --sk cipher.SecKey        if unspecified, a random key pair will be generated. (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --testenv                 whether to use production or test deployment service.

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")
```

##### Example defaults

The default visor config generation assumes the command is run from the root of the cloned repository

```
$ cd $GOPATH/src/github.com/skycoin/skywire
$ skywire-cli visor gen-config
[2021-06-24T08:58:56-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire-config.json"
[2021-06-24T08:58:56-05:00] INFO [skywire-cli]: Updated file '/home/user/go/src/github.com/skycoin/skywire/skywire-config.json' to: {
	"version": "v1.0.0",
	"sk": "b65d256d4a2af23e330179d95c9526a6e053479d8b5ca077ecf97dd8ec189876",
	"pk": "0336d57c96b706b8560223b0fa71e55331ab3dabe34dd464002ab10bf199ada2b3",
	"dmsg": {
		"discovery": "http://dmsg.discovery.skywire.skycoin.com",
		"sessions_count": 1
	},
	"dmsgpty": {
		"port": 22,
		"authorization_file": "./dmsgpty/whitelist.json",
		"cli_network": "unix",
		"cli_address": "/tmp/dmsgpty.sock"
	},
	"stcp": {
		"pk_table": null,
		"local_address": ":7777"
	},
	"transport": {
		"discovery": "http://transport.discovery.skywire.skycoin.com",
		"address_resolver": "http://address.resolver.skywire.skycoin.com",
		"log_store": {
			"type": "file",
			"location": "./transport_logs"
		},
		"trusted_visors": null
	},
	"routing": {
		"setup_nodes": [
			"0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
		],
		"route_finder": "http://routefinder.skywire.skycoin.com",
		"route_finder_timeout": "10s"
	},
	"uptime_tracker": {
		"addr": "http://uptime-tracker.skywire.skycoin.com"
	},
	"launcher": {
		"discovery": {
			"update_interval": "30s",
			"proxy_discovery_addr": "http://service.discovery.skycoin.com"
		},
		"apps": [
			{
				"name": "skychat",
				"args": [
					"-addr",
					":8001"
				],
				"auto_start": true,
				"port": 1
			},
			{
				"name": "skysocks",
				"auto_start": true,
				"port": 3
			},
			{
				"name": "skysocks-client",
				"auto_start": false,
				"port": 13
			},
			{
				"name": "vpn-server",
				"auto_start": false,
				"port": 44
			},
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "./apps",
		"local_path": "./local"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s"
}
```

The default configuration is for a visor only. To generate a configuration which provides the hypervisor web interface, the --is-hypervisor flag can be passed.
```
$ skywire-cli visor gen-config --is-hypervisor
```

##### Example hypervisor configuration for package based installation

This assumes the skywire installation is at /opt/skywire with binaries and apps in their own subdirectories.

```
$ cd /opt/skywire
$ skywire-cli visor gen-config --is-hypervisor -pro skywire.json
[2021-06-24T09:09:39-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/opt/skywire/skywire.json"
[2021-06-24T09:09:39-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/opt/skywire/skywire.json"
[2021-06-24T09:09:39-05:00] INFO [skywire-cli]: Updated file '/opt/skywire/skywire.json' to: {
	"version": "v1.0.0",
	"sk": "b65d256d4a2af23e330179d95c9526a6e053479d8b5ca077ecf97dd8ec189876",
	"pk": "0336d57c96b706b8560223b0fa71e55331ab3dabe34dd464002ab10bf199ada2b3",
	"dmsg": {
		"discovery": "http://dmsg.discovery.skywire.skycoin.com",
		"sessions_count": 1
	},
	"dmsgpty": {
		"port": 22,
		"authorization_file": "/opt/skywire/dmsgpty/whitelist.json",
		"cli_network": "unix",
		"cli_address": "/tmp/dmsgpty.sock"
	},
	"stcp": {
		"pk_table": null,
		"local_address": ":7777"
	},
	"transport": {
		"discovery": "http://transport.discovery.skywire.skycoin.com",
		"address_resolver": "http://address.resolver.skywire.skycoin.com",
		"log_store": {
			"type": "file",
			"location": "/opt/skywire/transport_logs"
		},
		"trusted_visors": null
	},
	"routing": {
		"setup_nodes": [
			"0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
		],
		"route_finder": "http://routefinder.skywire.skycoin.com",
		"route_finder_timeout": "10s"
	},
	"uptime_tracker": {
		"addr": "http://uptime-tracker.skywire.skycoin.com"
	},
	"launcher": {
		"discovery": {
			"update_interval": "30s",
			"proxy_discovery_addr": "http://service.discovery.skycoin.com"
		},
		"apps": [
			{
				"name": "skychat",
				"args": [
					"-addr",
					":8001"
				],
				"auto_start": true,
				"port": 1
			},
			{
				"name": "skysocks",
				"auto_start": true,
				"port": 3
			},
			{
				"name": "skysocks-client",
				"auto_start": false,
				"port": 13
			},
			{
				"name": "vpn-server",
				"auto_start": false,
				"port": 44
			},
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "/opt/skywire/apps",
		"local_path": "/opt/skywire/local"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"hypervisor": {
		"db_path": "/opt/skywire/users.db",
		"enable_auth": true,
		"cookies": {
			"hash_key": "fa985b3bb729fb4feedf3c7f6329cc31dcdca4457ac27d11cdeb14b39f248f01c5b784853074be3b95b02a9edeb2a6c721e0ee5675d1d55705cbd670082a107a",
			"block_key": "7d2f37467789f9bbd9f053b26a2580e03d2e36ecae0ca966ab6ae10a34b1bfbc",
			"expires_duration": 43200000000000,
			"path": "/",
			"domain": ""
		},
		"dmsg_port": 46,
		"http_addr": ":8000",
		"enable_tls": true,
		"tls_cert_file": "/opt/skywire/ssl/cert.pem",
		"tls_key_file": "/opt/skywire/ssl/key.pem"
	}
}

```

The configuration is written (or rewritten)

##### Example - visor configuration for package based installation

It is the typical arrangement to set a visor to use a remote hypervisor if a local instance is not started.


Determine the hypervisor public key by running the following command on the remote machine

```
_pubkey=$(cat /opt/skywire/skywire.json | grep pk\") _pubkey=${_pubkey#*: } ; echo $_pubkey
```

When running a visor with or without a hypervisor on the same machine, it's wise to keep the same keys for the other config file.

Copy the `skywire.json` config file from the previous example to `skywire-visor.json`; then paste the public key from the above command output into the following command

```
$ cd /opt/skywire
$ skywire-cli visor gen-config --hypervisor-pks <hypervisor-public-key> -pro skywire-visor.json
```

The configuration is written (or rewritten)

The configuration files may be specified in corresponding systemd service files or init / startup scripts to start either a visor or hypervisor instance

starting the hypervisor intance
```
skywire-visor -c /opt/skywire/skywire.json
```

starting visor-only or with remote hypervisor
```
skywire-visor -c /opt/skywire/skywire-visor.json
```

#### ls apps

list apps

```
$ skywire-cli visor ls-apps
```

##### Example

```
$ $ skywire-cli visor ls-apps
app                 ports     auto_start     status
skychat             1         true           running
skysocks            3         true           running
skysocks-client     13        false          stopped
vpn-server          44        false          stopped
vpn-client          43        false          stopped
```

#### ls rules

Lists the local visor's routing rules

```
$ skywire-cli visor ls-rules
```

##### Example

```
$ skywire-cli visor ls-rules
id     type     local-port     remote-port     remote-pk     resp-id     next-route-id     next-transport-id     expire-at
```


#### ls tp

list transports
```
$ skywire-cli visor ls-tp
```

##### Example

```
$ skywire-cli visor ls-tp
type     id     remote     mode     is_up
```

#### ls types

Lists transport types used by the local visor

```
$ skywire-cli visor ls-types
```

##### Example

```
$ skywire-cli visor ls-types
dmsg
stcp
stcpr
sudph
```

#### pk

Obtains the public key of the visor

```
$ skywire-cli visor pk
```

##### Example

```
$ skywire-cli visor pk                                                          
0359f02198933550ad5b41a21470a0bbe0f73c0eb6e93d7d279133a0d5bffc645c   
```

#### rm rule

Removes a routing rule

```
$ skywire-cli visor rm-rule <route-id>
```

##### Example

```
$ $ skywire-cli visor rm-rule -h
Removes a routing rule via route ID key

Usage:
  skywire-cli visor rm-rule <route-id> [flags]

```

#### rm tp

removes a transport
```
$ skywire-cli visor rm-tp <transport-id>
```

##### Example

```
$ $ skywire-cli visor rm-tp -h
Removes transport with given id

Usage:
  skywire-cli visor rm-tp <transport-id> [flags]

```


#### rule

```
$ skywire-cli visor rule <route-id>
```

##### Example

```
$ $ skywire-cli visor rule -h
Returns a routing rule via route ID key

Usage:
  skywire-cli visor rule <route-id> [flags]

```

#### set-app-autostart

set application autostart

```
$ skywire-cli visor set-app-autostart <name> (on|off)
```

##### Example

```
$ skywire-cli visor set-app-autostart -h
Sets the autostart flag for an app of given name

Usage:
  skywire-cli visor set-app-autostart <name> (on|off) [flags]
```

#### start-app

start application
```
$ skywire-cli visor set-app-autostart <name> (on|off)
```

##### Example

```
$ skywire-cli visor set-app-autostart -h
Sets the autostart flag for an app of given name

Usage:
  skywire-cli visor set-app-autostart <name> (on|off) [flags]

```

#### stop-app

stop application

```
$ skywire-cli visor stop app <name>
```

##### Example

```
$ skywire-cli visor stop-app skychat
OK
```


#### tp

Returns summary of given transport by id

```
$ skywire-cli visor tp <transport-id>
```

##### Example

```
$ skywire-cli visor tp -h
Returns summary of given transport by id

Usage:
  skywire-cli visor tp <transport-id> [flags]
```


#### update config

```
$ skywire-cli visor update-config -h
Updates a config file

Usage:
  skywire-cli visor update-config [flags]

Flags:
      --add-hypervisor-pks string   public keys of hypervisors that should be added to this visor
  -e, --environment string          desired environment (values production or testing) (default "production")
  -h, --help                        help for update-config
  -i, --input string                path of input config file. (default "skywire-config.json")
  -o, --output string               path of output config file. (default "skywire-config.json")
      --reset-hypervisor-pks        resets hypervisor`s configuration

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")

```

##### Example

```
skywire-cli visor update-config
[2021-06-24T10:42:33-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="skywire-config.json"
[2021-06-24T10:42:33-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="skywire-config.json"
[2021-06-24T10:42:33-05:00] INFO [skywire-cli]: Updated file '/home/d0mo/go/src/github.com/skycoin/skywire/skywire-config.json' to: {
 "version": "v1.0.0",
 "sk": "24db3a001c62baaa5b1a05d3f903e708f9ac0d7ed8d49bc4f1223c6fa94d91d1",
 "pk": "02c8ddafcf4d88c0734b98919c4f924ddb542dbba815b0d804e2e0518fa954f096",
 "dmsg": {
	 "discovery": "http://dmsg.discovery.skywire.skycoin.com",
	 "sessions_count": 1
 },
 "dmsgpty": {
	 "port": 22,
	 "authorization_file": "./dmsgpty/whitelist.json",
	 "cli_network": "unix",
	 "cli_address": "/tmp/dmsgpty.sock"
 },
 "stcp": {
	 "pk_table": null,
	 "local_address": ":7777"
 },
 "transport": {
	 "discovery": "http://transport.discovery.skywire.skycoin.com",
	 "address_resolver": "http://address.resolver.skywire.skycoin.com",
	 "log_store": {
		 "type": "file",
		 "location": "./transport_logs"
	 },
	 "trusted_visors": null
 },
 "routing": {
	 "setup_nodes": [
		 "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	 ],
	 "route_finder": "http://routefinder.skywire.skycoin.com",
	 "route_finder_timeout": "10s"
 },
 "uptime_tracker": {
	 "addr": "http://uptime-tracker.skywire.skycoin.com"
 },
 "launcher": {
	 "discovery": {
		 "update_interval": "30s",
		 "proxy_discovery_addr": "http://service.discovery.skycoin.com"
	 },
	 "apps": [
		 {
			 "name": "skychat",
			 "args": [
				 "-addr",
				 ":8001"
			 ],
			 "auto_start": true,
			 "port": 1
		 },
		 {
			 "name": "skysocks",
			 "auto_start": true,
			 "port": 3
		 },
		 {
			 "name": "skysocks-client",
			 "auto_start": false,
			 "port": 13
		 },
		 {
			 "name": "vpn-server",
			 "auto_start": false,
			 "port": 44
		 },
		 {
			 "name": "vpn-client",
			 "auto_start": false,
			 "port": 43
		 }
	 ],
	 "server_addr": "localhost:5505",
	 "bin_path": "./apps",
	 "local_path": "./local"
 },
 "hypervisors": [],
 "cli_addr": "localhost:3435",
 "log_level": "info",
 "shutdown_timeout": "10s",
 "restart_check_delay": "1s"
}
```


#### version        

version

```
$ skywire-cli visor version
```

##### Example

```
$ skywire-cli visor version
Version "0.4.1" built on "2021-03-19T23:26:21Z" against commit "d804a8ce"
```


### rtfind usage

```
skywire-cli rtfind <public-key-visor-1> <public-key-visor-2>
```

##### Example

```
$ skywire-cli rtfind -h

Queries the Route Finder for available routes between two visors

Usage:
skywire-cli rtfind <public-key-visor-1> <public-key-visor-2> [flags]

Flags:
--addr string        address in which to contact route finder service (default "http://routefinder.skywire.skycoin.com")
-h, --help               help for rtfind
--max-hops uint16    max hops for the returning routeFinderRoutesCmd (default 1000)
--min-hops uint16    min hops for the returning routeFinderRoutesCmd (default 1)
--timeout duration   timeout for remote server requests (default 10s)
```
