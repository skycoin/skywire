# skywire cli config gen

[← skywire cli config](../README.md)

Generate a config file

	Custom deployment config (services-config.json) may be specified with:
	SKYDEPLOY=/path/to/services-config.json skywire cli config gen
	This overrides the embedded deployment defaults for all service URLs,
	DMSG servers, and DMSG endpoints. Use with --nofetch to skip HTTP fetch.

## Usage

```
skywire cli config gen
```

## Flags

```
  -n, --stdout                               write config to stdout
  -N, --squash                               output config without whitespace or newlines
  -o, --out string                           output config: skywire-config.json
  -w, --hide                                 suppress config output to terminal
      --wallet-custody string                where the skycoin-web wallet keeps keys: browser|disk|remote
      --wallet-dir string                    wallet dir for --wallet-custody=disk (a seed-wallet store, not per-coin)
      --wallet-remote string                 visor PK holding the wallets, for --wallet-custody=remote
      --no-wallet                            do not serve the skycoin-web wallet frontend
  -q, --envs                                 show the conf template (reflects flags passed)
  -Q, --envout string                        write conf template to file (reflects flags passed)
  -f, --force                                remove pre-existing config
  -r, --regen                                regenerate existing config and retain keys
  -x, --retainhv                             retain existing hypervisors with regen
  -a, --url string                           services conf url
                                              (default "dmsg://021f751cb8690a96585e10c4d253513cd208bd659fd4f6c227ad49d2b75eec1ff2:80")
  -t, --testenv                              use test deployment (ports offset +10000 from prod)
  -d, --dmsghttp                             use only dmsg for skywire services, no http (this is the default)
  -D, --dmsgconf string                      dmsghttp config path
      --nofetch                              do not fetch the services from the service conf url
  -S, --svcconf string                       fallback service configuration file
      --nodefaults                           do not use hardcoded defaults for services
      --minsess int                          number of dmsg servers to connect to (0 = unlimited) (default 2)
  -y, --autoconn                             disable autoconnect to public visors
  -z, --public                               publicize visor in service discovery
      --stcpr int                            tcp transport listening port (0 = random / shared master port)
      --sudph int                            udp transport listening port (0 = random / shared master port)
      --transport-port int                   shared master transport port for all transport types (0 = per-type ports)
      --min-hops int                         minimum route hops (1 = allow direct 1-hop routes, >=2 = force multihop through intermediaries for sender privacy) (default 1)
      --ar-transport-limit int               address-resolver registration: 0 = stay registered, N>0 = deregister after N transports, N<0 = never register (inbound-invisible)
      --no-direct-transports                 never create direct p2p transports; dmsg (relay) is still allowed
      --pty-rpc-exec                         allow visor-RPC-initiated dmsgpty exec (control/jump node opt-in; off = closes a local privilege-escalation vector)
      --routesetup string                    add route setup node PKs
      --tpsetup string                       add transport setup node PKs
      --cascade                              opt into source-driven cascade route setup (default: legacy setup-node path)
      --sn                                   generate config for route setup node
      --dmsgdisc                             generate config for dmsg-discovery service
      --dmsgsrv                              generate config for dmsg-server service
      --tpd                                  generate config for transport-discovery service
      --sd                                   generate config for service-discovery service
      --ar                                   generate config for address-resolver service
      --rf                                   generate config for route-finder service
      --calculate-routes                     enable local route calculation
  -i, --ishv                                 local hypervisor configuration
  -j, --hvpks string                         list of public keys to add as hypervisor
  -c, --noauth                               disable authentication for hypervisor UI
  -e, --auth                                 enable auth on hypervisor UI
      --pk-endpoint                          expose unauthenticated GET /api/pk on the hypervisor (skybian / Arch-ARM image builds set this)
      --dmsgpty string                       add dmsgpty whitelist PKs
      --survey string                        add survey whitelist PKs
  -l, --publicip                             display visor ip in service discovery
  -m, --example-apps                         add example apps to the config
      --external-apps                        configure launcher apps as external processes
  -g, --disableapps string                   comma separated list of apps to disable
      --binpath string                       set bin_path for visor native apps
  -v, --servevpn                             autostart vpn server (default true)
      --servevpnrouter                       autostart the vpn router (LAN/WiFi gateway that NATs into the vpn-client tunnel; needs --vpnrouter-lan-ifc)
      --vpnrouter-lan-ifc string             downstream LAN/WiFi interface the vpn router serves (e.g. eth1 or wlan0)
      --vpnrouter-subnet string              vpn router gateway+subnet as <gateway-ip>/<prefix> (default 192.168.42.1/24)
      --vpnrouter-wifi                       vpn router WiFi-out: run hostapd (AP) on the downstream interface
      --vpnrouter-ssid string                vpn router WiFi SSID (with --vpnrouter-wifi)
      --vpnrouter-passphrase string          vpn router WiFi WPA2 passphrase, 8–63 chars (with --vpnrouter-wifi)
      --vpnrouter-band string                vpn router WiFi band: 2.4 or 5 (default 2.4)
      --vpnrouter-channel int                vpn router WiFi channel (0 = default for the band)
      --vpnrouter-country string             vpn router WiFi regulatory country code (default US)
      --vpnrouter-open                       vpn router WiFi: allow an open (passphrase-less) network
      --vpnrouter-mesh-gateway               vpn router: also act as a mesh gateway (resolve *.dmsg / *.skynet for clients)
      --vpnrouter-mesh-gateway-cidr string   vpn router mesh-gateway synthetic-IP pool (default 100.64.0.0/16)
      --vpnrouter-mesh-gateway-tls           vpn router mesh gateway: TLS-MITM HTTPS to *.dmsg/*.skynet (clients must trust the generated CA)
      --killsw string                        vpn client killswitch
      --addvpn string                        set vpn server public key for vpn client
      --vpnwl string                         vpn server whitelist (comma separated; empty allows all)
      --secure string                        change secure mode status of vpn server
      --netifc string                        VPN Server network interface (detected: anpi1, anpi0, en3, en4, en1, en2, bridge0, ap1, en0, awdl0, llw0, utun0, utun1, utun2, utun3, utun4)
      --proxyclientpk string                 set server public key for proxy client
      --startproxyclient                     autostart proxy client
      --serveproxy                           autostart proxy server (default true)
      --proxywl string                       proxy server whitelist (comma separated; empty allows all)
      --dmsgweb                              enable embedded .dmsg resolving SOCKS5 proxy on 127.0.0.1:4445
      --skynetweb                            enable embedded .skynet resolving SOCKS5 proxy on 127.0.0.1:4446
      --skymail-bridge                       enable SMTP to skywire bridge on 127.0.0.1:1025
      --dmsgweb-upstream string              upstream SOCKS5 for non .dmsg traffic (empty chains to skynetweb)
      --skynetweb-upstream string            upstream SOCKS5 for non .skynet traffic
      --dmsgweb-addr string                  host the .dmsg SOCKS5 proxy binds to (empty=127.0.0.1; 0.0.0.0 or a LAN IP to serve the LAN)
      --skynetweb-addr string                host the .skynet SOCKS5 proxy binds to (empty=127.0.0.1; 0.0.0.0 or a LAN IP to serve the LAN)
      --servechat                            autostart skychat (default true)
      --chataddr string                      skychat local address (default "127.0.0.1:8001")
      --servechatpair                        skychat pair RPC channel (required for group chat) (default true)
      --chatportless                         run skychat with no TCP port; reach its UI only through the hypervisor
      --skycoind                             autostart skycoin daemon (legacy single instance)
      --skycoindfiber string                 legacy FIBER_TOML path (single instance)
      --skycoindapi string                   legacy api sets (single instance)
      --skycoindUSER string                  skycoin daemon UID (applies to every instance)
      --skycoindinstances string             skycoin daemon instances (comma separated; skycoin or fiber.toml path)
      --skycoindflags string                 extra flags appended to every skycoin daemon (port and data dir auto allocated)
      --coin-nodes string                    fibercoin nodes to forward over dmsg + advertise (type=coin); CSV of local_addr[@dmsg_port]
      --skycoinweb                           autostart skycoin web wallet (thin client)
      --skycoinwebaddr string                skycoin web bind address (host:port) (default "127.0.0.1:8002")
      --skycoinwebnodes string               node URLs the skycoin web wallet talks to (comma separated)
      --skycoinwebwallet string              skycoin web wallet dir override
      --skycoinwebuser string                skycoin web UID (empty inherits visor UID)
      --rewardaddr string                    skycoin reward address or xpub key
  -k, --os string                            (linux / mac / win) paths (default "mac")
  -p, --pkg                                  use mac installation path: /Library/Application Support/Skywire
  -u, --user                                 use paths for user space: /Users/mohammed
      --loglvl string                        level of logging in config (default "info")
  -s, --sk cipher.SecKey                     a random key is generated if unspecified
                                              (default 0000000000000000000000000000000000000000000000000000000000000000)
      --version string                       custom version testing override
      --hvaddr string                        hypervisor HTTP address
      --stun string                          comma-separated list of STUN servers
      --timeout string                       graceful shutdown timeout (e.g. 10s)
      --regtimeout string                    public visor registration timeout (e.g. 10m)
      --maxtransports int                    public visor max transports
      --muxroutes int                        number of parallel mux routes per connection
      --cliaddr string                       CLI RPC address (e.g. 0.0.0.0:3435 for Docker)
      --lan-dmsg-port int                    embedded DMSG server TCP port (0 = OS-assigned at runtime; pin via LANDMSGPORT for stable WAN reachability)
      --lan-dmsg-public string               embedded DMSG server WAN-reachable address (host:port; requires port-forward)
      --all                                  show all flags
```

## Global Flags

```
  -h, --help              show help menu
      --json              print output as JSON
      --via dmsg://<pk>   remote visor target — dmsg://<pk> or `skynet://<pk>`
```

---
_Generated by `skywire doc` — do not edit by hand._
