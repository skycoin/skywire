# CLI Documentation

skywire command line interface

<!-- MarkdownTOC autolink="true" bracket="round" levels="1,2,3" -->

- [Install](#install)
- [skywire-cli usage](#skywire-cli-usage)
    - [config usage](#config-usage)
        - [gen](#config-gen)
        - [update](#config-update)
    - [visor usage](#visor-usage)
        - [exec](#exec)
        - [pk](#pk)
        - [hv](#hv)
        - [info](#summary)
        - [version](#version)
        - [app](#app)
            - [ls](#app-ls)
            - [autostart](#app-autostart)
            - [start](#app-start)
            - [stop](#app-stop)
        - [route](#route)
            - [ls rules](#route-ls-rules)
            - [rule](#route-rule)
            - [add rule](#route-add-rule)
            - [rm rule](#route-rm-rule)
        - [tp](#tp)
            - [type](#tp-type)
            - [disc](#tp-disc)
            - [id](#tp-id)
            - [ls](#tp-ls)
            - [add](#tp-add)
            - [rm](#tp-rm)
        - [vpn](#vpn)
            - [ui](#vpn-ui)
            - [url](#vpn-url)
        - [update](#visor-update)
    - [rtfind usage](#rtfind-usage)
    - [mdisc usage](#mdisc-usage)
        - [servers](#servers)
        - [entry](#entry)
    - [completion usage](#completion-usage)


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
  config      Generate or update a skywire config
  visor       Query the Skywire Visor
  rtfind      Query the Route Finder
  mdisc       Query remote DMSG Discovery
  completion  Generate completion script
  help        Help about any command

Flags:
  -h, --help   help for skywire-cli

Use "skywire-cli [command] --help" for more information about a command.
```

### config usage

```
skywire-cli config -h
Generate or update a skywire config

Usage:
  skywire-cli config [command]

Available Commands:
  gen         generate a config file
  update      update a config file

Flags:
  -h, --help   help for config

Use "skywire-cli config [command] --help" for more information about a command.
```

#### config gen

```
$ skywire-cli config gen --help
generate a config file

Usage:
  skywire-cli config gen [flags]

Flags:
  -b, --best-proto            determine best protocol (dmsg / direct) based on location
  -c, --disable-auth          disable authentication for hypervisor UI.
  -d, --dmsghttp              use dmsg connection to skywire services
  -e, --enable-auth           enable auth on hypervisor UI.
  -f, --disable-apps string   comma separated list of apps to disable
  -i, --is-hv                 hypervisor configuration.
  -j, --hvpks string          comma separated list of public keys to use as hypervisor
  -k, --os string             use os-specific paths (linux / macos / windows) (default "linux")
  -o, --output string         path of output config file. (default "skywire-config.json")
  -p, --package               use paths for package (/opt/skywire)
  -q, --public-rpc            allow rpc requests from LAN.
  -r, --replace               rewrite existing config & retain keys.
  -s, --sk cipher.SecKey      if unspecified, a random key pair will be generated.
                               (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --testenv               use test deployment service.
  -v, --serve-vpn             enable vpn server.
  -x, --retain-hv             retain existing hypervisors with replace
  -h, --help                  help for gen
```

##### Example defaults

The default visor config generation assumes the command is run from the root of the cloned repository

<details>

<summary>
`$ cd $GOPATH/src/github.com/skycoin/skywire && skywire-cli config gen`
</summary>

```
[2022-03-13T22:20:56-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.1.1" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire-config.json"
[2021-06-24T08:58:56-05:00] INFO [skywire-cli]: Updated file '/home/user/go/src/github.com/skycoin/skywire/skywire-config.json' to:  
{
	"version": "v1.1.1",
	"sk": "b8d0fa5298d4aa658e616f6411efad6fd1edf4246111c3f6181e550871aa9e4b",
	"pk": "022acd91dba410f7b403af9b85ea5ecf20af3e69d307e065d0958a2bee5f969dc6",
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
		"transport_setup_nodes": null
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
		"apps": [
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			},
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
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "./apps"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"local_path": "./local",
	"stun_servers": [
		"45.118.133.242:3478",
		"192.53.173.68:3478",
		"192.46.228.39:3478",
		"192.53.113.106:3478",
		"192.53.117.158:3478",
		"192.53.114.142:3478",
		"139.177.189.166:3478",
		"192.46.227.227:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"dmsghttp_path": "./dmsghttp-config.json",
	"persistent_transports": null
}
```
</details>

The default configuration is for a visor only. To generate a configuration which provides the hypervisor web interface,
the `-i` or `--is-hypervisor` flag should be specified.

<details>

<summary>
`$ skywire-cli config gen -i`
</summary>

```
{
	"version": "v1.1.1",
	"sk": "18446280e0c2f52b31e74b9000357098cd6fabf5ab7db5ecc1aad59d6064daf9",
	"pk": "0381649f174fc04c5f28ea118a8e21f6baed84894aef8f590afc36e149c0178bf5",
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
		"transport_setup_nodes": null
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
		"apps": [
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			},
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
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "./apps"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"local_path": "./local",
	"stun_servers": [
		"45.118.133.242:3478",
		"192.53.173.68:3478",
		"192.46.228.39:3478",
		"192.53.113.106:3478",
		"192.53.117.158:3478",
		"192.53.114.142:3478",
		"139.177.189.166:3478",
		"192.46.227.227:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"dmsghttp_path": "./dmsghttp-config.json",
	"persistent_transports": null,
	"hypervisor": {
		"db_path": "/home/user/go/src/github.com/skycoin/skywire/users.db",
		"enable_auth": false,
		"cookies": {
			"hash_key": "825940e3d8b05d121fafce176787614065639f72d6a9b8bec7dec8e12891d8e044a607084aad53a07b1536e5503566fdd3c30f14612404f07e7972c63db9c53b",
			"block_key": "f2c298106bf4f9841213bf74c5d7bf861f7b341c6e7769da0f6dc5ccc32a5780",
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
</details>


Note that it is possible to start the visor with the hypervisor interface explicitly now, regardless of how the config was generated; using the -f flag

```
skywire-visor -i
```

##### Example package based installation defaults

This assumes the skywire installation is at `/opt/skywire` with binaries and apps in their own subdirectories.

<details>

<summary>
`$ sudo skywire-cli config gen -bipr --enable-auth`
</summary>

```
{
	"version": "v1.1.1",
	"sk": "25c0376a6701cccbe640533586664f1d5d5fbaf5bb28017801296b301a55195a",
	"pk": "03151a6d3c41832a4da0b8b59fe47c9adada4785091a0003012fd720ec9a4ec5c4",
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
		"transport_setup_nodes": null
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
		"apps": [
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			},
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
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "/opt/skywire/apps"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"local_path": "/opt/skywire/local",
	"stun_servers": [
		"45.118.133.242:3478",
		"192.53.173.68:3478",
		"192.46.228.39:3478",
		"192.53.113.106:3478",
		"192.53.117.158:3478",
		"192.53.114.142:3478",
		"139.177.189.166:3478",
		"192.46.227.227:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"dmsghttp_path": "/opt/skywire/dmsghttp-config.json",
	"persistent_transports": null,
	"hypervisor": {
		"db_path": "/opt/skywire/users.db",
		"enable_auth": true,
		"cookies": {
			"hash_key": "367232d15908204a81a69428cbba4b56af31ff977966f828b327efd5f01c61937db4893d3e1f5cda183524b576cf66274e796b02744863d0b13edabd0ebdace8",
			"block_key": "6a6ef06af73306a2af38184dba9d75d8cb5c24ba97cf98b58543d7bbec5e21ed",
			"expires_duration": 43200000000000,
			"path": "/",
			"domain": ""
		},
		"dmsg_port": 46,
		"http_addr": ":8000",
		"enable_tls": false,
		"tls_cert_file": "/opt/skywire/ssl/cert.pem",
		"tls_key_file": "/opt/skywire/ssl/key.pem"
	}
```
</details>

The configuration is written (or rewritten)

##### Example remote hypervisor configuration for package based installation

The typical arrangement uses a remote hypervisor if a local instance is not started.

The hypervisor public key can be determined by running the following command on the designated hypervisor

```
$ skywire-cli visor pk
```

When running a visor with or without a hypervisor on the same machine, it's wise to keep the same keys for any persistent
configuration.

Copy the `skywire.json` config file from the previous example to `skywire-visor.json`; then paste the public key from
the above command output into the following command

```
# cp /opt/skywire/skywire.json /opt/skywire/skywire-visor.json
# skywire-cli config gen -j <hypervisor-public-key> -bpr
```

The configuration is regenerated


##### Example running with systemd service integration

The configuration files described above are specified in corresponding systemd service files in the skywire-bin .deb and archlinux packages to manage a visor or hypervisor instance

hypervisor

```
# skywire-visor -c /opt/skywire/skywire.json
```


with remote hypervisor

```
# skywire-visor -c /opt/skywire/skywire-visor.json
```

#### config update

```
$ skywire-cli config update --help
update a config file

Usage:
  skywire-cli config update [flags]

Flags:
      --add-hypervisor-pks string           public keys of hypervisors that should be added to this visor
      --add-skysocks-client-server string   add skysocks server address to skysock-client
      --add-skysocks-passcode string        add passcode to skysocks server
      --add-vpn-client-passcode string      add passcode of server if needed
      --add-vpn-client-server string        add server address to vpn-client
      --add-vpn-server-passcode string      add passcode to vpn-server
  -e, --environment string                  desired environment (values production or testing) (default "production")
  -h, --help                                help for update
  -i, --input string                        path of input config file. (default "skywire-config.json")
  -o, --output string                       path of output config file. (default "skywire-config.json")
      --reset-hypervisor-pks                resets hypervisor configuration
      --reset-skysocks                      reset skysocks configuration
      --reset-skysocks-client               reset skysocks-client configuration
      --reset-vpn-client                    reset vpn-client configurations
      --reset-vpn-server                    reset vpn-server configurations
      --set-minhop int                      change min hops value (default -1)
      --set-public-autoconnect string       change public autoconnect configuration
      --vpn-client-killswitch string        change killswitch status of vpn-client
      --vpn-server-secure string            change secure mode status of vpn-server
```

##### Example

<details>

<summary>
```
$ skywire-cli config update
```
</summary>



```
$ skywire-cli config update
[2022-03-13T22:31:24-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.1.1" filepath="skywire-config.json"
[2022-03-13T22:31:24-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.1.1" filepath="skywire-config.json"
[2022-03-13T22:31:24-05:00] INFO [skywire-cli]: Updated file '/home/user/go/src/github.com/skycoin/skywire/skywire-config.json' to: {
	"version": "v1.1.1",
	"sk": "9642958732a9c21cbebeb65cced9689856cdacea84d0e72895bb9c76c2fca0b2",
	"pk": "024c4b7c4f3d2f51128a08d7fbd526be9427b2cfbc486da0340b57e3b86e36bdf4",
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
		"transport_setup_nodes": null
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
		"apps": [
			{
				"name": "vpn-client",
				"auto_start": false,
				"port": 43
			},
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
			}
		],
		"server_addr": "localhost:5505",
		"bin_path": "./apps"
	},
	"hypervisors": [],
	"cli_addr": "localhost:3435",
	"log_level": "info",
	"local_path": "./local",
	"stun_servers": [
		"45.118.133.242:3478",
		"192.53.173.68:3478",
		"192.46.228.39:3478",
		"192.53.113.106:3478",
		"192.53.117.158:3478",
		"192.53.114.142:3478",
		"139.177.189.166:3478",
		"192.46.227.227:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"dmsghttp_path": "./dmsghttp-config.json",
	"persistent_transports": null
}
```
</details>






### visor usage

```
$ skywire-cli visor -h
Query the Skywire Visor

Usage:
  skywire-cli visor [command]

Available Commands:
  exec        execute a command
  pk          public key of the visor
  hv          show hypervisor(s)
  info        summary of visor info
  version     version and build info
  app         app settings
  route       view and set rules
  tp          view and set transports
  vpn         vpn interface
  update      update the local visor

Flags:
  -h, --help         help for visor
      --rpc string   RPC server address (default "localhost:3435")

Use "skywire-cli visor [command] --help" for more information about a command.
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

escape a flag

```
$skywire-cli visor exec echo -- "-a"
-a
```

#### pk

public key of the visor

```
$ skywire-cli visor pk
```

```
Flags:
  -i, --input string   path of input config file.
```

##### Example

```
$ skywire-cli visor pk                                                          
0359f02198933550ad5b41a21470a0bbe0f73c0eb6e93d7d279133a0d5bffc645c   
```

#### hv

show hypervisor(s)

```
$ skywire-cli visor hv
```

```
Flags:
  -i, --input string   path of input config file.
```

##### Example

```
$ skywire-cli visor hv
[0359f02198933550ad5b41a21470a0bbe0f73c0eb6e93d7d279133a0d5bffc645c]
```

#### info

summary of visor info

```
$ skywire-cli visor info
```

##### Example

```
$ skywire-cli visor info
.:: Visor Summary ::.
Public key: "034b68c4d8ec6d934d3ecb28595fea7e89a8de2048f0f857759c5018cb8e2f9525"
Symmetric NAT: false
IP: 192.168.0.2
DMSG Server: "0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13"
Ping: "451.449714ms"
Visor Version: v0.6.0
Skybian Version:
Uptime Tracker: healthy
Time Online: 37102.342894 seconds
Build Tag: linux_amd64
```


#### version

version and build info

```
$ skywire-cli visor version
```

##### Example

```
$ skywire-cli visor version
Version "v0.6.0" built on "2022-02-17T11:18:39Z" against commit "b8b70310"
```

#### app

```
$ skywire-cli visor app
app settings

Usage:
  skywire-cli visor app [command]

Available Commands:
  ls          list apps
  start       launch app
  stop        halt app
  autostart   set autostart flag for app
  log         logs from app since RFC3339Nano-formated timestamp.
                    "beginning" is a special timestamp to fetch all the logs

Flags:
  -h, --help   help for app

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")

Use "skywire-cli visor app [command] --help" for more information about a command.
```

##### app ls

list apps

```
$ skywire-cli visor app ls
```

##### Example

```
$ skywire-cli visor app ls
app                 ports     auto_start     status
skychat             1         true           running
skysocks            3         true           running
skysocks-client     13        false          stopped
vpn-server          44        false          stopped
vpn-client          43        false          stopped
```

#### app start

start application

```
$ skywire-cli visor app start <name>
```

##### Example

```
$ skywire-cli visor app start vpn-server
OK
```

#### app stop

stop application

```
$ skywire-cli visor app stop <name>
```

##### Example

```
$ skywire-cli visor app stop skychat
OK
```


#### app autostart

set autostart flag for app

```
$ skywire-cli visor app autostart <name> (on|off)
```

##### Example

```
$ skywire-cli visor app autostart vpn-server on
OK
```

#### app logs

logs from app since RFC3339Nano-formated timestamp.
                    "beginning" is a special timestamp to fetch all the logs

```
$ skywire-cli visor app logs <name> <timestamp>
```

##### Example

```
$ skywire-cli visor app log skysocks beginning [2022-03-11T21:15:55-06:00] INFO [public_autoconnect]: Fetching public visors
 [2022-03-11T21:16:06-06:00] INFO [public_autoconnect]: Fetching public visors
 [2022-03-11T21:16:09-06:00] INFO [dmsgC]: Session stopped. error="failed to serve dialed session to 0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13: EOF"
 [2022-03-11T21:16:09-06:00] WARN [dmsgC]: Stopped accepting streams. error="EOF" session=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
 [2022-03-11T21:16:10-06:00] INFO [dmsgC]: Dialing session... remote_pk=0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb
 [2022-03-11T21:16:14-06:00] INFO [dmsgC]: Serving session. remote_pk=0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb
```

#### route

```
$ skywire-cli visor route
view and set rules

Usage:
  skywire-cli visor route [command]

Available Commands:
  ls-rules    list routing rules
  rule        return routing rule by route ID key
  rm-rule     remove routing rule
  add-rule    add routing rule

Flags:
  -h, --help   help for route

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")

Use "skywire-cli visor route [command] --help" for more information about a command.
```

#### route add-rule

```
$ skywire-cli visor route add-rule (app <route-id> <local-pk> <local-port> <remote-pk> <remote-port> | fwd <next-route-id> <next-transport-id>) [flags]
```

##### Example

```
$ skywire-cli visor route add-rule -h
add routing rule

Usage:
  skywire-cli visor route add-rule (app <route-id> <local-pk> <local-port> <remote-pk> <remote-port> | fwd <next-route-id> <next-transport-id>) [flags]

Flags:
  -h, --help                  help for add-rule
      --keep-alive duration   duration after which routing rule will expire if no activity is present (default 30s)

```

#### route rm-rule

Removes a routing rule

```
$ skywire-cli visor route rm-rule <route-id>
```

##### Example

```
$ skywire-cli visor route rm-rule -h
Removes a routing rule via route ID key

Usage:
  skywire-cli visor rm-rule <route-id> [flags]

```

#### route ls-rules

list routing rules

```
$ skywire-cli visor route ls-rules
```

#### route rule

```
$ skywire-cli visor route rule <route-id>
```

##### Example

```
$ skywire-cli visor route rule -h
Returns a routing rule via route ID key

Usage:
  skywire-cli visor route rule <route-id> [flags]

```

#### tp

```
view and set transports

Usage:
  skywire-cli visor tp [command]

Available Commands:
  disc        discover transport(s) by ID or public key
  type        transport types used by the local visor
  ls          available transports
  id          transport summary by id
  add         add a transport
  rm          remove transport(s) by id

Flags:
  -h, --help   help for tp

Global Flags:
      --rpc string   RPC server address (default "localhost:3435")

Use "skywire-cli visor tp [command] --help" for more information about a command.

```

#### tp add

add transport

```
$ skywire-cli visor tp add <remote-public-key> [flags]
```

##### Example

```
$ skywire-cli visor tp add -h
Adds a new transport

Usage:
  skywire-cli visor add-tp <remote-public-key> [flags]

Flags:
  -h, --help               help for add-tp
      --public             whether to make the transport public (deprecated)
  -t, --timeout duration   if specified, sets an operation timeout
      --type string        type of transport to add; if unspecified, cli will attempt to establish a transport in the following order: stcp, stcpr, sudph, dmsg
```

#### tp disc

discover transport(s) by ID or public key

```
$ skywire-cli visor tp disc (--id=<transport-id> | --pk=<edge-public-key>)
```

##### Example

```
$ skywire-cli visor tp disc -h
discover transport(s) by ID or public key

Usage:
  skywire-cli visor tp disc (--id=<transport-id> | --pk=<edge-public-key>) [flags]

Flags:
  -h, --help               help for disc-tp
      --id transportID     if specified, obtains a single transport of given ID (default 00000000-0000-0000-0000-000000000000)
      --pk cipher.PubKey   if specified, obtains transports associated with given public key (default 000000000000000000000000000000000000000000000000000000000000000000)

```

#### tp id

transport summary by id

```
$ skywire-cli visor tp id <transport-id>
```

##### Example

```
$ skywire-cli visor tp id -h
transport summary by id

Usage:
skywire-cli visor tp <transport-id> [flags]
```

#### tp ls

list transports

```
$ skywire-cli visor tp ls
```

##### Example

```
$ skywire-cli visor tp ls
type     id     remote     mode     is_up
```

#### tp type

Lists transport types used by the local visor

```
$ skywire-cli visor tp type
```

##### Example

```
$ skywire-cli visor tp type
dmsg
stcp
stcpr
sudph
```

#### tp rm

remove transport

```
$ skywire-cli visor tp rm <transport-id>
```

##### Example

```
$ skywire-cli visor tp rm -h
Removes transport with given id

Usage:
  skywire-cli visor tp rm <transport-id> [flags]

```


#### vpn

vpn interface


```
$   skywire-cli visor vpn [command]
```

##### Example

```
$ skywire-cli visor vpn -h

vpn interface

Usage:
  skywire-cli visor vpn [command]

Available Commands:
  ui          Open VPN UI in default browser
  url         Show VPN UI URL

```

#### vpn ui

Open VPN UI in default browser

```
$   skywire-cli visor vpn ui
```


##### Example

```
$ skywire-cli visor vpn ui
```

the VPN user interface is opened in the default browser

#### vpn url

Show VPN UI URL

```
$   skywire-cli visor vpn url
```


##### Example

```
$ skywire-cli visor vpn url
http://127.0.0.1:8000/#/vpn/027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed/
```


#### update

update

```
$ skywire-cli visor update
```

### rtfind usage

```
skywire-cli rtfind <public-key-visor-1> <public-key-visor-2>
```

##### Example

```
$ skywire-cli rtfind -h

Query the Route Finder

Usage:
  skywire-cli rtfind <public-key-visor-1> <public-key-visor-2> [flags]

Flags:
  -a, --addr string        route finder service address (default "http://rf.skywire.skycoin.com")
  -h, --help               help for rtfind
  -x, --max-hops uint16    maximum hops (default 1000)
  -n, --min-hops uint16    minimum hops (default 1)
  -t, --timeout duration   request timeout (default 10s)
```

### mdisc usage

```
Query remote DMSG Discovery

Usage:
  skywire-cli mdisc [command]

Available Commands:
  entry       fetch an entry
  servers     fetch available servers

Flags:
      --addr string   address of DMSG discovery server
                       (default "http://dmsgd.skywire.skycoin.com")
  -h, --help          help for mdisc

  Use "skywire-cli mdisc [command] --help" for more information about a command.
```

#### servers

```
$ skywire-cli mdisc servers
```

```
Flags:
      --addr string   address of DMSG discovery server
      (default "http://dmsgd.skywire.skycoin.com")
```

##### Example

```
$ skywire-cli mdisc server
[2022-03-13T21:10:44-05:00] DEBUG disc.NewHTTP [mdisc:disc]: Created HTTP client. addr="http://dmsgd.skywire.skycoin.com"
version     registered              public-key                                                             address                                           available-sessions
0.0.1       1647224020460616235     02347729662a901d03f1a1ab6c189a173349fa11e79fe82117cca0f8d0e4d64a31     192.53.115.181:8082                               2582
0.0.1       1647224015059832662     02e4660279c83bc6ca0122d3a78c0cb3f3564e03e04876ae7fa30b4e0a63217425     192.53.115.181:8081                               1299
0.0.1       1647224018690620887     02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd     dmsg.server02a2d4c3.skywire.skycoin.com:30081     1109
0.0.1       1647224019967944735     0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13     192.53.114.142:30086                              582
0.0.1       1647224016544544252     0228af3fd99c8d86a882495c8e0202bdd4da78c69e013065d8634286dd4a0ac098     45.118.133.242:30084                              48
0.0.1       1647224021047139719     03717576ada5b1744e395c66c2bb11cea73b0e23d0dcd54422139b1a7f12e962c4     dmsg.server03717576.skywire.skycoin.com:30082     31
0.0.1       1647224018229901714     0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb     192.53.114.142:30085                              19
0.0.1       1647224017051283856     02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0     dmsg.server02a4.skywire.skycoin.com:30089         1

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
$ skywire-cli mdisc entry 034b68c4d8ec6d934d3ecb28595fea7e89a8de2048f0f857759c5018cb8e2f9525
[2022-03-13T21:17:11-05:00] DEBUG disc.NewHTTP [mdisc:disc]: Created HTTP client. addr="http://dmsgd.skywire.skycoin.com"
	version: 0.0.1
	sequence: 4
	registered at: 1647205336195743639
	static public key: 034b68c4d8ec6d934d3ecb28595fea7e89a8de2048f0f857759c5018cb8e2f9525
	signature: 7a7cee456a17b13207a8eba6dd60102505e0d5b3b98f047225da8bfc8e963a557c75fbbba5c7654835230c9372d6faae2f7570bb71b1af9d36cbdc4da195b74701
	entry is registered as client. Related info:
		delegated servers:
			0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
```


### completion usage

```
#skywire-cli completion
```

```
To load completions

Usage:
  skywire-cli completion [bash|zsh|fish|powershell]

Flags:
  -h, --help   help for completion
```
