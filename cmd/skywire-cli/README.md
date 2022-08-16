# CLI Documentation

skywire command line interface

<!-- MarkdownTOC autolink="true" bracket="round" levels="1,2,3" -->

- [Install](#install)
- [skywire-cli usage](#skywire-cli-usage)
	- [global flags](#global-flags)
    - [config usage](#config-usage)
        - [gen](#config-gen)
        - [update](#config-update)
	- [dmsgpty usage](#dmsgpty-usage)
		- [dmsgpty ui](#dmsgpty-ui)
		- [dmsgpty url](#dmsgpty-url)
		- [dmsgpty list](#dmsgpty-list)
		- [dmsgpty start](#dmsgpty-start)
	- [visor usage](#visor-usage)
		- [app](#visor-app)
			- [ls](#app-ls)
			- [autostart](#app-autostart)
			- [start](#app-start)
			- [stop](#app-stop)
		- [exec](#visor-exec)
		- [hvui](#visor-hvui)
        - [pk](#visor-pk)
		- [hvpk](#visor-hvpk)
		- [chvpk](#visor-chvpk)
        - [info](#visor-info)
        - [version](#visor-version)
        - [route](#visor-route)
            - [ls rules](#route-ls-rules)
            - [rule](#route-rule)
            - [add rule](#route-add-rule)
            - [rm rule](#route-rm-rule)
		- [halt](#visor-halt)
		- [start](#visor-start)
        - [tp](#visor-tp)
            - [type](#tp-type)
            - [disc](#tp-disc)
            - [id](#tp-id)
            - [ls](#tp-ls)
            - [add](#tp-add)
            - [rm](#tp-rm)
    - [vpn](#vpn-usage)
		- [list](#vpn-list)
		- [ui](#vpn-ui)
		- [url](#vpn-url)
		- [start](#vpn-start)
		- [stop](#vpn-stop)
		- [status](#vpn-status)
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

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴

Usage:
  skywire-cli [command]

Available Commands:
  config       Generate or update a skywire config
  dmsgpty      Interact with remote visors
  visor        Query the Skywire Visor
  vpn          controls for VPN client
  rtfind       Query the Route Finder
  mdisc        Query remote DMSG Discovery
  completion   Generate completion script

Flags:
      --rpc string   RPC server address (default "localhost:3435")
  -v, --version      version for skywire-cli

Use "skywire-cli [command] --help" for more information about a command.
```
### global flags

The skywire-cli interacts with the running visor via rpc calls. By default the rpc server is available on localhost:3435. The rpc address and port the visor is using may be changed in the config file, once generated.

It is not recommended to expose the rpc server on the local network.
Exposing the rpc allows unsecured access to the machine over the local network

```
Global Flags:
      --rpc string   RPC server address (default "localhost:3435")
```

### config usage

A primary function of skywire-cli is generating and updating the config file used by skywire-visor.

```
$ skywire-cli config -h
Generate or update a skywire config

Usage:
  skywire-cli config [command]

Available Commands:
  gen          Generate a config file
  update       Update a config file
```

#### config gen

```
$ skywire-cli config gen --help
Generate a config file

Usage:
  skywire-cli config gen [flags]

Flags:
  -b, --bestproto      best protocol (dmsg | direct) based on location
  -i, --ishv           local hypervisor configuration
  -j, --hvpks string   list of public keys to use as hypervisor
  -o, --out string     output config: skywire-config.json
  -p, --pkg            use path for package: /opt/skywire
  -u, --user           use paths for user space: /home/user
  -r, --regen          re-generate existing config & retain keys
  -y, --autoconn       disable autoconnect to public visors
      --all            show all flags

$ skywire-cli config gen --all
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
  -n, --stdout               write config to stdout
  -o, --out string           output config: skywire-config.json
  -p, --pkg                  use path for package: /opt/skywire
  -u, --user                 use paths for user space: /home/user
  -q, --publicrpc            allow rpc requests from LAN
  -r, --regen                re-generate existing config & retain keys
  -s, --sk cipher.SecKey     a random key is generated if unspecified
 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --testenv              use test deployment conf.skywire.dev
  -v, --servevpn             enable vpn server
  -w, --hide                 dont print the config to the terminal
  -x, --retainhv             retain existing hypervisors with regen
  -y, --autoconn             disable autoconnect to public visors
  -z, --public               publicize visor in service discovery
      --version string       custom version testing override
      --binpath string       set bin_path

```

##### Example defaults

The default visor config generation assumes the command is run from the root of the cloned repository.
The example further assumes the compiled binary is available in the executable PATH and that GOPATH is set

<details>

<summary>
cd $GOPATH/src/github.com/skycoin/skywire && skywire-cli config gen
</summary>

```
$ cd $GOPATH/src/github.com/skycoin/skywire
$ skywire-cli config gen
[2022-08-15T10:32:29-05:00] INFO []: Fetched service endpoints from 'http://conf.skywire.skycoin.com'
[2022-08-15T10:32:29-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire-config.json"
[2022-08-15T10:32:29-05:00] INFO [skywire-cli]: Updated file 'skywire-config.json' to:
{
	"version": "v1.0.0",
	"sk": "b0b193b38cb970b36bfe4bb05e2354ae96a979283b18bcf516f22a86436ddea3",
	"pk": "034b7c206b825e896e727a43ae8685d7d93460ec0f37d4e3bbc3c44e6e771a8455",
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
		"192.46.224.108:3478",
		"139.177.185.210:3478",
		"139.162.17.54:3478",
		"139.162.17.107:3478",
		"139.162.17.156:3478",
		"45.118.134.168:3478",
		"139.177.185.180:3478",
		"139.162.17.48:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null
}

```
</details>

##### Example hypervisor defaults

The default configuration is for a visor only. To generate a configuration which provides the hypervisor web interface,
the `-i` or `--is-hypervisor` flag should be specified.

<details>

<summary>
skywire-cli config gen -i
</summary>

```
$ skywire-cli config gen -i
[2022-08-15T10:33:18-05:00] INFO []: Fetched service endpoints from 'http://conf.skywire.skycoin.com'
[2022-08-15T10:33:18-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire-config.json"
[2022-08-15T10:33:18-05:00] INFO [skywire-cli]: Updated file 'skywire-config.json' to:
{
	"version": "v1.0.0",
	"sk": "b0b193b38cb970b36bfe4bb05e2354ae96a979283b18bcf516f22a86436ddea3",
	"pk": "034b7c206b825e896e727a43ae8685d7d93460ec0f37d4e3bbc3c44e6e771a8455",
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
		"192.46.224.108:3478",
		"139.177.185.210:3478",
		"139.162.17.54:3478",
		"139.162.17.107:3478",
		"139.162.17.156:3478",
		"45.118.134.168:3478",
		"139.177.185.180:3478",
		"139.162.17.48:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null,
	"hypervisor": {
		"db_path": "/home/user/go/src/github.com/skycoin/skywire/users.db",
		"enable_auth": false,
		"cookies": {
			"hash_key": "cd6918c54f4b7b11b50cc395f57b1f78fafa5d08daa6917b246501eeeb00d9ee5557aedf79513f3392c5ffdd5a073711c7a0243460930615424ecfa75f2d3db1",
			"block_key": "41caf4a9935b9f48d37fff1b8e950f165e73c29d4effd548def4382c4dff842a",
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


Note that it is possible to start the visor with the hypervisor interface explicitly; regardless of how the config was generated; using the -i flag

```
skywire-visor -i
```

##### Example dmsghttp defaults

Using dmsghttp routes http traffic used for connecting to the skywire services through dmsg.

The dmsghttp-config.json file must be present to generate a config with dmsghttp

The `-b` or `--bestproto` flag will automatically determine if dmsghttp should be used based on region

The `-d` or `--dmsghttp` flag explicitly creates the config with dmsghttp

It is recommended to use the `-b` flag for config file generation.

The example below uses `-d` to create a dmsghttp config

<details>

<summary>
skywire-cli config gen -d
</summary>

```
[2022-08-15T10:34:30-05:00] INFO [skywire-cli]: Found Dmsghttp config: dmsghttp-config.json
[2022-08-15T10:34:31-05:00] INFO []: Fetched service endpoints from 'http://conf.skywire.skycoin.com'
[2022-08-15T10:34:31-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire-config.json"
[2022-08-15T10:34:31-05:00] INFO [skywire-cli]: Updated file 'skywire-config.json' to:
{
	"version": "v1.0.0",
	"sk": "b0b193b38cb970b36bfe4bb05e2354ae96a979283b18bcf516f22a86436ddea3",
	"pk": "034b7c206b825e896e727a43ae8685d7d93460ec0f37d4e3bbc3c44e6e771a8455",
	"dmsg": {
		"discovery": "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80",
		"sessions_count": 1,
		"servers": [
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd",
				"server": {
					"address": "dmsg.server02a2d4c3.skywire.skycoin.com:30081",
					"availableSessions": 0
				}
			},
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "03717576ada5b1744e395c66c2bb11cea73b0e23d0dcd54422139b1a7f12e962c4",
				"server": {
					"address": "dmsg.server03717576.skywire.skycoin.com:30082",
					"availableSessions": 0
				}
			},
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "0228af3fd99c8d86a882495c8e0202bdd4da78c69e013065d8634286dd4a0ac098",
				"server": {
					"address": "45.118.133.242:30084",
					"availableSessions": 0
				}
			},
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "03d5b55d1133b26485c664cf8b95cff6746d1e321c34e48c9fed293eff0d6d49e5",
				"server": {
					"address": "dmsg.server03d5b55d.skywire.skycoin.com:30083",
					"availableSessions": 0
				}
			},
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
				"server": {
					"address": "192.53.114.142:30085",
					"availableSessions": 0
				}
			},
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0",
				"server": {
					"address": "dmsg.server02a4.skywire.skycoin.com:30089",
					"availableSessions": 0
				}
			},
			{
				"version": "",
				"sequence": 0,
				"timestamp": 0,
				"static": "02113579604c79b704e169a4fd94fd78167b86fe40da1016f8146935babcc9abcb",
				"server": {
					"address": "194.147.142.202:30050",
					"availableSessions": 0
				}
			}
		]
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
		"discovery": "dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80",
		"address_resolver": "dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80",
		"public_autoconnect": true,
		"transport_setup_nodes": null
	},
	"routing": {
		"setup_nodes": [
			"0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
		],
		"route_finder": "dmsg://039d89c5eedfda4a28b0c58b0b643eff949f08e4f68c8357278081d26f5a592d74:80",
		"route_finder_timeout": "10s",
		"min_hops": 0
	},
	"uptime_tracker": {
		"addr": "dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80"
	},
	"launcher": {
		"service_discovery": "dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80",
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
		"192.46.224.108:3478",
		"139.177.185.210:3478",
		"139.162.17.54:3478",
		"139.162.17.107:3478",
		"139.162.17.156:3478",
		"45.118.134.168:3478",
		"139.177.185.180:3478",
		"139.162.17.48:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null
}
```
</details>


##### Example package based installation defaults

This assumes the skywire linux installation is at `/opt/skywire` with binaries and apps in their own subdirectories.
The `-p` flag default paths are provided by the skywire linux / mac packages or windows .msi installer and generate the skywire config within the install dir.


<details>

<summary>
sudo skywire-cli config gen -bipr
</summary>

```
$ sudo skywire-cli config gen -bipr
[sudo] password for user:
[2022-08-15T10:35:37-05:00] INFO []: Fetched service endpoints from 'http://conf.skywire.skycoin.com'
[2022-08-15T10:35:37-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/opt/skywire/skywire.json"
[2022-08-15T10:35:37-05:00] INFO [skywire-cli]: Updated file '/opt/skywire/skywire.json' to:
{
	"version": "v1.0.0",
	"sk": "4aaa3cc266cb8c4ec9689a0d88c49802646517ae2d2240315c480d461c6af70e",
	"pk": "0291b61ab823eebe79575d6d0f8e122e84cd17dae1cab5c7dfb9043f1ee4f0a206",
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
		"192.46.224.108:3478",
		"139.177.185.210:3478",
		"139.162.17.54:3478",
		"139.162.17.107:3478",
		"139.162.17.156:3478",
		"45.118.134.168:3478",
		"139.177.185.180:3478",
		"139.162.17.48:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null,
	"hypervisor": {
		"db_path": "/opt/skywire/users.db",
		"enable_auth": true,
		"cookies": {
			"hash_key": "2e0337acde4de0c92531c39293839ca5b5398409b2da250c30ed89c814bdf6aab6a3e7ab2cd21e077e5d694997811e90be0de8552ad59a788a480d6d2efdf512",
			"block_key": "19035ce3ee7110f6d794bf02bab4fb6ed7360db9b725331da2732999745ddcd3",
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

The configuration is written (or rewritten)

##### Example remote hypervisor configuration for package based installation

The typical arrangement uses a remote hypervisor if a local instance is not started.

The desired hypervisor public key can be determined by running the following command on the running hypervisor:

```
$ skywire-cli visor pk
```

configure the visor to use the public key of the remote hypervisor:

```
# skywire-cli config gen -bprj <hypervisor-public-key>
```

The configuration is regenerated

#### config update

```
$ skywire-cli config update --help
Update a config file

Usage:
  skywire-cli config update [flags]
  skywire-cli config update [command]

Available Commands:
  hv           update hypervisor config
  sc           update skysocks-client config
  ss           update skysocks-server config
  vpnc         update vpn-client config
  vpns         update vpn-server config

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

##### Example

<details>

<summary>
skywire-cli config update
</summary>


```
$ skywire-cli config update
[2022-08-15T10:44:31-05:00] INFO [visor:config]: Flushing config to file. config_version="v1.0.0" filepath="/home/user/go/src/github.com/skycoin/skywire/skywire-config.json"
[2022-08-15T10:44:31-05:00] INFO [skywire-cli]: Updated file '/home/user/go/src/github.com/skycoin/skywire/skywire-config.json' to: {
	"version": "v1.0.0",
	"sk": "b0b193b38cb970b36bfe4bb05e2354ae96a979283b18bcf516f22a86436ddea3",
	"pk": "034b7c206b825e896e727a43ae8685d7d93460ec0f37d4e3bbc3c44e6e771a8455",
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
		"192.46.224.108:3478",
		"139.177.185.210:3478",
		"139.162.17.54:3478",
		"139.162.17.107:3478",
		"139.162.17.156:3478",
		"45.118.134.168:3478",
		"139.177.185.180:3478",
		"139.162.17.48:3478"
	],
	"shutdown_timeout": "10s",
	"restart_check_delay": "1s",
	"is_public": false,
	"persistent_transports": null
}
```
</details>

### dmsgpty usage

The dmsgpty is a means of accessing remote visors which are connected to a locally running hypervisor via the shell.

One can think of it as similar in functionality to ssh. The difference is that the connection, from a remote visor to the hypervisor, is already established.

```
$ skywire-cli dmsgpty
Interact with remote visors

Usage:
  skywire-cli dmsgpty [command]

Available Commands:
  ui           Open dmsgpty UI in default browser
  url          Show dmsgpty UI URL
  list         List connected visors
  start        Start dmsgpty session

```

#### dmsgpty ui

The dmsgpty ui is accessible from the hypervisor UI, and requires that one has already logged into the hypervisor UI or that the session cookie for the hypervisor UI exists.

Open dmsgpty UI in default browser

```
$ skywire-cli dmsgpty ui -h
Usage:
  skywire-cli dmsgpty ui [flags]
```

```
Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to
```

#### dmsgpty url

Show dmsgpty UI URL
```
$ skywire-cli dmsgpty url
```

```
Usage:
  skywire-cli dmsgpty url [flags]

Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to

```

#### dmsgpty list

The visors which are shown by this command are currently connected to the hypervisor

List connected visors

```
$  skywire-cli dmsgpty list
```

#### dmsgpty start

A public key of a connected remote visor must be provided as an argument. The list command, above, lists remote visors which are connected to the locally running hypervisor.

Start dmsgpty session

```
$ skywire-cli dmsgpty start <pk>
```

```
Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")

```

Starting the dmsgpty-cli will give access via the default shell to the remote visor, as the same user which started the remote visor.

### visor usage

```
$ skywire-cli visor
Query the Skywire Visor

Usage:
  skywire-cli visor [command]

Available Commands:
  app          App settings
  exec         Execute a command
  hvui         Hypervisor UI
  pk           Public key of the visor
  hvpk         Public key of remote hypervisor
  chvpk        Public key of connected hypervisors
  info         Summary of visor info
  version      Version and build info
  route        View and set rules
  halt         Stop a running visor
  start        Start a visor
  tp           View and set transports

```

#### visor exec

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


#### visor hvui

open the hypervisor UI in the default browser
```
$ skywire-cli visor hvui
```

#### visor pk

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

#### visor hvpk

Public key of remote hypervisor(s) set in the config

```
$ skywire-cli visor hvpk
```

```

Usage:
  skywire-cli visor hvpk [flags]

Flags:
  -w, --http           serve public key via http
  -i, --input string   path of input config file.
  -p, --pkg            read from /opt/skywire/skywire.json

```

##### Example

```
$ skywire-cli visor hvpk
[0359f02198933550ad5b41a21470a0bbe0f73c0eb6e93d7d279133a0d5bffc645c]
```

#### visor chvpk

show conected hypervisor(s)

```
$ skywire-cli visor chvpk
```

##### Example

```
$ skywire-cli visor chvpk
[0359f02198933550ad5b41a21470a0bbe0f73c0eb6e93d7d279133a0d5bffc645c]
```

#### visor info

summary of visor info

```
$ skywire-cli visor info
```

##### Example

```
$ skywire-cli visor info
.:: Visor Summary ::.
Public key: "038229af479f87c8132e84884487b8985f55b49c8e0aa17ac715f0270678c33f2a"
Symmetric NAT: false
IP: 192.168.254.130
DMSG Server: "0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13"
Ping: "477.203238ms"
Visor Version: unknown
Skybian Version:
Uptime Tracker: healthy
Time Online: 4242.082701 seconds
Build Tag:

```

#### visor version

version and build info

```
$ skywire-cli visor version
```

##### Example

```
$ skywire-cli visor version
Version "v1.0.0" built on "2022-05-26T18:18:39Z" against commit "668d5ad8"
```

#### visor app

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
$ skywire-cli visor app log skysocks beginning
 [2022-03-11T21:15:55-06:00] INFO [public_autoconnect]: Fetching public visors
 [2022-03-11T21:16:06-06:00] INFO [public_autoconnect]: Fetching public visors
 [2022-03-11T21:16:09-06:00] INFO [dmsgC]: Session stopped. error="failed to serve dialed session to 0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13: EOF"
 [2022-03-11T21:16:09-06:00] WARN [dmsgC]: Stopped accepting streams. error="EOF" session=0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
 [2022-03-11T21:16:10-06:00] INFO [dmsgC]: Dialing session... remote_pk=0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb
 [2022-03-11T21:16:14-06:00] INFO [dmsgC]: Serving session. remote_pk=0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb
```

#### visor route

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

#### visor tp

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


#### visor start

Start a visor
```
$ skywire-cli visor start
```

```
Flags:
  -s, --src   'go run' external commands from the skywire sources
```

#### visor halt

Stop a running visor

```
$ skywire-cli visor halt
```

### vpn usage

vpn interface


```
$   skywire-cli vpn
controls for VPN client

Usage:
  skywire-cli vpn [command]

Available Commands:
  list         List public VPN servers
  ui           Open VPN UI in default browser
  url          Show VPN UI URL
  start        start the vpn for <public-key>
  stop         stop the vpn
  status       vpn status

```

#### vpn list

The vpn list subcommand queries the list of [public VPN servers](https://sd.skycoin.com/api/services?type=vpn) from the service discovery, with optional filters for country and version.

List [public VPN servers](https://sd.skycoin.com/api/services?type=vpn)

```
Usage:
  skywire-cli vpn list [flags]

Flags:
  -c, --country string   filter results by country
  -n, --nofilter         provide unfiltered results
  -s, --stats            return only a count of the resuts
  -y, --systray          format results for systray
  -v, --ver string       filter results by version

```

##### Example

```
$ skywire-cli vpn list -s
293 VPN Servers

$ skywire-cli vpn list
[
	{
		"address": "0214948797b58e60febb3c9f977c92203474a3fde470c28fe0b2adc91bf6f3015b:44",
		"type": "vpn",
		"geo": {
			"lat": 0.52,
			"lon": 101.44,
			"country": "ID",
			"region": "RI"
		},
		"version": "v0.6.0"
	},
	{
		"address": "0231fb93b9f5b4e2b9a71a58aa35165b16eaae7df764a839561d008e221d58b148:44",
		"type": "vpn",
		"geo": {
			"lat": 40.5992,
			"lon": -77.5768,
			"country": "US",
			"region": "PA"
		},
		"version": "v0.5.1"
	},
	{
		"address": "02fc1c9e9e78c644e0818cbfbd66585c9c8d664ada0fbf1b89c1afd42178739559:44",
		"type": "vpn",
		"geo": {
			"lat": 33.14,
			"lon": -96.75,
			"country": "US",
			"region": "TX"
		},
		"version": "v1.0.0"
	},
	{
		"address": "029fffe4ffc40d15d2d739b21c8d69c1a7b63e72ad9a6827c6c370cea01dc8e5d4:44",
		"type": "vpn",
		"geo": {
			"lat": 46.88,
			"lon": -71.34,
			"country": "CA",
			"region": "QC"
		},
		"version": "v1.0.1"
	},

$ skywire-cli vpn list -y
03287341eb55277a0e8a1c20328900fe955d9fa184989c10dc4218e494e77d7bf3 | GR
031888409f7d5ae26c0dd46b970cc06d75d1b494feacac7c152008811e3c5cc797 | ID
021e95f178a1cace6658d22fa9445101c7001531c75b444fc2f1b92d44bfbba753 | ID
039b61cb88caf9d18104cd661a242607ba174f33fa5df548e6eb5308414002f570 | CA
021d2bb4e3f414bb39fbe2a2f273004b55e611ebfb8fcff7d0795340945e27e36f | US
0277b420e9abeae438a98c63c175ad3f5ba6f02181eb230f1b00eaf16858eef71b | NL
0278df107bdcade217f0a75330122f7d9df3e43f2635686824506f61b21fae2fb5 | BH
03b99bbebafb5dcb0035faf86d84766355fb989da8497ad7128d789bc511e46e52 | ID
02921cd31fc4aaec49b6f460ec87103cfd931cecd6b84007c5d66b1e7af1ba98b8 | CA
03a367c310c4d921a3315dbc3940673f32dcbecb401439c20aeb713558cf6726b2 | US
03fbbacd70dcc16d4336f006d5a5316a4a3e0ea21839ea70228ae164921a731d53 | GR
0280b94366b93d3f145b4ee7a5c4e36a23ee673043a0d9bb8d69fd983fedbf67c6 | ID
03ec7911e471ce4da2ede75c0c1cfe0ead7416c77bfac8b94d8d2456d9d7148abc

$ skywire-cli vpn list -c US -v 1.0.1
[
	{
		"address": "03a367c310c4d921a3315dbc3940673f32dcbecb401439c20aeb713558cf6726b2:44",
		"type": "vpn",
		"geo": {
			"lat": 39.37,
			"lon": -104.86,
			"country": "US",
			"region": "CO"
		},
		"version": "v1.0.1"
	},
	{
		"address": "0356c02912e2df48afe47b258b98e28b2dea3dd04eb9a6b0d4975ee962959c3834:44",
		"type": "vpn",
		"geo": {
			"lat": 36,
			"lon": -83.91,
			"country": "US",
			"region": "TN"
		},
		"version": "v1.0.1"
	},
	{
		"address": "0311112186dabc0371dce9b8ae0d1a7b4429ec5b8197dd316d3a67b6ec8d5acb9a:44",
		"type": "vpn",
		"geo": {
			"lat": 37.76,
			"lon": -122.49,
			"country": "US",
			"region": "CA"
		},
		"version": "v1.0.1"
	},
	{
		"address": "023d87ddc1ceb2cff04315781a7a70cc8ee7c664a532e428de8da994e815608f1c:44",
		"type": "vpn",
		"geo": {
			"lat": 37.76,
			"lon": -122.49,
			"country": "US",
			"region": "CA"
		},
		"version": "v1.0.1"
	},
	{
		"address": "024034dd09cbff03787db740124496ce8fdbe0cac2a51e20366cb59be10b665506:44",
		"type": "vpn",
		"geo": {
			"lat": 33.14,
			"lon": -96.75,
			"country": "US",
			"region": "TX"
		},
		"version": "v1.0.1"
	},
	{
		"address": "025530eb5e5fd04c1c91d02b405787552d71bde13c9946d33a9a43acf67b7031fc:44",
		"type": "vpn",
		"geo": {
			"lat": 37.76,
			"lon": -122.49,
			"country": "US",
			"region": "CA"
		},
		"version": "v1.0.1"
	},
...
```

#### vpn start

The vpn start subcommand requires a vpn server public key as argument.
A key may be selected from the output of `skywire-cli vpn list`

```
$ skywire-cli vpn start -h
start the vpn for <public-key>

Usage:
  skywire-cli vpn start [flags]

```

#### vpn stop

stop the vpn

```
$ skywire-cli vpn stop -h

Usage:
  skywire-cli vpn stop [flags]

```

#### vpn status

vpn status

```
Usage:
  skywire-cli vpn status [flags]
```

##### Example

```
$ skywire-cli vpn status
stopped
```


#### vpn ui

Open VPN UI in default browser

```
$   skywire-cli vpn ui
```


##### Example

```
$ skywire-cli vpn ui
```

the VPN user interface is opened in the default browser

#### vpn url

Show VPN UI URL

```
$   skywire-cli vpn url
```


##### Example

```
$ skywire-cli visor vpn url
http://127.0.0.1:8000/#/vpn/027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed/
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

```
