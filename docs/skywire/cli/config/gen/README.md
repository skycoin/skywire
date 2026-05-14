# skywire cli config gen

[← skywire cli config](../README.md)

Generate a config file

	Config defaults file may also be specified with:
	SKYENV=/path/to/skywire.conf skywire-cli config gen
	print the SKYENV file template with:
	skywire-cli config gen -q

## Usage

```
skywire cli config gen
```

## Flags

```
  -n, --stdout                      write config to stdout
  -N, --squash                      output config without whitespace or newlines
  -o, --out string                  output config: skywire-config.json
  -w, --hide                        dont print the config to the terminal :: show errors with -n flag
  -q, --envs                        show the conf template (reflects flags passed)
  -Q, --envout string               write conf template to file (reflects flags passed)
  -f, --force                       remove pre-existing config
  -r, --regen                       re-generate existing config & retain keys
  -x, --retainhv                    retain existing hypervisors with regen
  -a, --url string                  services conf url
                                     (default "http://conf.skywire.skycoin.com")
  -t, --testenv                     use test deployment
                                    (ports are offset +10000 to allow running alongside prod)
  -d, --dmsghttp                    use only dmsg connection to skywire services (no http fallback)
      --http                        use only http connection to skywire services (no dmsg)
  -D, --dmsgconf string             dmsghttp-config path
      --nofetch                     do not fetch the services from the service conf url
  -S, --svcconf string              fallback service configuration file
      --nodefaults                  do not use hardcoded defaults for services
      --minsess int                 number of dmsg servers to connect to (0 = unlimited) (default 2)
  -y, --autoconn                    disable autoconnect to public visors
  -z, --public                      publicize visor in service discovery
      --stcpr int                   set tcp transport listening port - 0 for random
      --sudph int                   set udp transport listening port - 0 for random
      --routesetup string           add route setup node PKs
      --tpsetup string              add transport setup node PKs
      --sn                          generate config for route setup node
      --dmsgdisc                    generate config for dmsg-discovery service
      --dmsgsrv                     generate config for dmsg-server service
      --tpd                         generate config for transport-discovery service
      --sd                          generate config for service-discovery service
      --ar                          generate config for address-resolver service
      --rf                          generate config for route-finder service
      --calculate-routes            enable local route calculation
  -i, --ishv                        local hypervisor configuration
  -j, --hvpks string                list of public keys to add as hypervisor
  -c, --noauth                      disable authentication for hypervisor UI
  -e, --auth                        enable auth on hypervisor UI
      --dmsgpty string              add dmsgpty whitelist PKs
      --survey string               add survey whitelist PKs
  -l, --publicip                    display visor ip in service discovery
  -m, --example-apps                add example apps to the config
      --external-apps               configure launcher apps as external processes
  -g, --disableapps string          comma separated list of apps to disable
      --binpath string              set bin_path for visor native apps
  -v, --servevpn                    autostart vpn server (default: true) (default true)
      --killsw string               vpn client killswitch
      --addvpn string               set vpn server public key for vpn client
      --vpnwl string                comma-separated list of public keys allowed to connect to vpn server (empty = allow all)
      --secure string               change secure mode status of vpn server
      --netifc string               VPN Server network interface (detected: eth0)
      --proxyclientpk string        set server public key for proxy client
      --startproxyclient            autostart proxy client
      --serveproxy                  autostart proxy server (default: true) (default true)
      --proxywl string              comma-separated list of public keys allowed to connect to proxy server (empty = allow all)
      --dmsgweb                     enable embedded .dmsg resolving SOCKS5 proxy on 127.0.0.1:4445
      --skynetweb                   enable embedded .skynet resolving SOCKS5 proxy on 127.0.0.1:4446
      --dmsgweb-upstream string     upstream SOCKS5 for non-.dmsg traffic (e.g. 127.0.0.1:1080); empty + skynetweb on = auto-chain to skynetweb
      --skynetweb-upstream string   upstream SOCKS5 for non-.skynet traffic (e.g. 127.0.0.1:1080)
      --servechat                   autostart skychat (default: true) (default true)
      --chataddr string             skychat local address (default "127.0.0.1:8001")
      --skycoind                    autostart skycoin daemon (full node) — single-instance legacy path; superseded by --skycoindinstances when set
      --skycoindfiber string        legacy single-instance FIBER_TOML; superseded by --skycoindinstances
      --skycoindapi string          legacy single-instance --enable-gui-api-sets; superseded by --skycoindflags
      --skycoindUSER string         drop skycoin daemon to this user via launcher external-mode (applies to every instance)
      --skycoindinstances string    comma-separated list of skycoin-daemon instances; entries are either 'skycoin' (built-in defaults) or a fiber.toml path. Empty = use the legacy single-instance path driven by --skycoind / --skycoindfiber.
      --skycoindflags string        extra flags appended to every skycoin-daemon instance (e.g. '--enable-gui-api-sets READ,STATUS'); --port and --data-dir are auto-allocated and must not appear here
      --skycoinweb                  autostart skycoin-web thin-client wallet
      --skycoinwebaddr string       skycoin-web bind address (host:port) (default "127.0.0.1:8002")
      --skycoinwebnodes string      comma-separated node URLs the skycoin-web wallet talks to (empty = upstream default at https://node.skycoin.com)
      --skycoinwebwallet string     skycoin-web --wallet-dir override (empty = upstream default at ~/.skycoin/wallets)
      --skycoinwebuser string       drop skycoin-web to this user via launcher external-mode (POSIX setuid; empty = inherit visor UID)
      --rewardaddr string           skycoin reward address or xpub key
  -k, --os string                   (linux / mac / win) paths (default "linux")
  -p, --pkg                         use path for package: /opt/skywire
  -u, --user                        use paths for user space: /home/skywire
      --loglvl string               level of logging in config (default "info")
  -s, --sk cipher.SecKey            a random key is generated if unspecified
                                     (default 0000000000000000000000000000000000000000000000000000000000000000)
      --version string              custom version testing override
      --hvaddr string               hypervisor HTTP address
      --stun string                 comma-separated list of STUN servers
      --timeout string              graceful shutdown timeout (e.g. 10s)
      --regtimeout string           public visor registration timeout (e.g. 10m)
      --maxtransports int           public visor max transports
      --muxroutes int               number of parallel mux routes per connection
      --cliaddr string              CLI RPC address (e.g. 0.0.0.0:3435 for Docker)
      --lan-dmsg-port int           embedded DMSG server TCP port (0 = OS-assigned at runtime; pin via LANDMSGPORT for stable WAN reachability)
      --lan-dmsg-public string      embedded DMSG server WAN-reachable address (host:port; requires port-forward)
      --all                         show all flags
```

## Global Flags

```
  -h, --help   show help menu
      --json   print output as JSON
```

---
_Generated by `skywire doc` — do not edit by hand._
