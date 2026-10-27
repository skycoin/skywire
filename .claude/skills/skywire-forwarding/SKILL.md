---
name: skywire-forwarding
description: Skywire forwarding and proxy apps. The skysocks proxy (proxy start, proxy mux), skynet port forwarding, serve (exposing ports on .skynet and .dmsg), the resolving SOCKS proxy (resolver), vpn and got, plus the forwarding port and app wiring architecture. Load for exposing or forwarding ports or proxying over the mesh.
---

# Forwarding and proxy

Flags change often. Run `<cmd> --help` for the current list and `skywire doc` for the generated reference.

## skysocks proxy client
- `skywire cli proxy start [pk]` or `--pk/-k <server-pk>`. The SOCKS5 listener is `127.0.0.1:1080` unless `--addr/-a` says otherwise. Test it with `curl -x socks5h://127.0.0.1:1080 http://<url>`.
- A session stripes browser connections over `--tunnels N` independent tunnels (default 2, which adds up their bandwidth). `--standby-pool` holds spare tunnels ready for failover, and -1 (default) picks automatically. `--tunnels 1` uses a single tunnel over the AppDirect shortcut with no route group.
- `--direct` forces one direct transport leg and bypasses the routing policy and mux. It cannot combine with `--routing-policy`, `--route`, `--min-hops` above 1 or `--tunnels` above 1.
- `--route <file>` pins explicit routes (the shape `route calc --count N --json` prints). `--routing-policy preset:<x>|@file.star|@file.wasm|none` sets the per app policy. `--min-hops N` forces intermediates. `--mux-mode auto|equal` sets the weighting. `--reconnect` (default true) keeps the listener and re-dials after a route group collapse.
- `proxy stop --name/-n <n>` or `--all`. One of them is required. `proxy status` shows each client and its route legs. A single leg is flagged because it has no failover.
- `proxy list` reads service discovery. `proxy test` probes servers and has its own short flags (`-s` is `--pk`, `-k` is `--country`).
- `proxy server start|status|stop` controls the local skysocks exit app. It is public when advertised in service discovery and private when `--whitelist/-w` is set.
- A skysocks exit refuses to dial its own public IP, so a loopback test needs the exit and the target on different hosts.
- `proxy settings` shows or sets live tuning knobs. `proxy log` streams route events.

## proxy mux
- `proxy mux info [-n app] [--watch 1s] [--ndjson FILE --duration D]` shows per leg traffic, latency and health. It is read only.
- `proxy mux plot` draws a live chart. With `--pk <peer> --routes N` it drives a controlled mux measurement.
- Others are `events`, `negotiated`, `weights`, `direction`, `width`, `cap`, `standby`, `grow`, `adopt`.
- These change a live route group, so do not run them against a session someone is using: `mux set --legs f.json [--prune]`, `add`, `rm <tp-id>`, `mode auto|equal`, `auto <preset>`. Use `--rg <port>` to pick one tunnel, where the port is the `dst_port` that `mux info` prints.
- `proxy loadtest serve` and `proxy loadtest run` are a steady load rig for policy tests.

## skynet port forwarding
- Server: `skywire cli skynet srv start -p <port>[,<port>] -w <client-pk-csv>`. Client: `skynet start [pk] --pk/-k <srv-pk> --remote/-r <rport> --local/-l <lport>`. `--routes N` sets parallel routes, `--direct`, `--min-hops`, `--forward-mux`, `--reverse-mux` and `--routing-policy` also exist. Also `skynet status` and `skynet stop`.
- The client dials the far end's built in forwarding service. Port 57 (`SkyForwardingServerPort`) is raw. Port 59 (`SkyForwardingMuxPort`) is yamux multiplexed. The resolver dials 59 through a warm route pool and is fast. An app that dials 57 over an app client works for direct and single routes, but mux routes to 57 can carry the handshake and then no data.
- Two wiring patterns exist. Launcher apps dial through an app client. In process closures over the router (resolver, pty) are the better template for forwarding.
- `skynet port` is deprecated in favor of `serve`. It still holds the only UDP forwards: `skynet port add <p> --udp` on the server, `skynet port udp-dial <pk> <rport>`, `udp-ls`, `udp-stop <lport>` on the client.

## serve
- `skywire cli serve add <port> --to <host:port|port>` exposes a local port on the visor's .skynet and .dmsg face. Options are `--label`, `--desc`, `--skynet`, `--dmsg`, `--landing`, `--preserve-host`, `--inject-pk`, `--whitelist <pk-csv>`.
- `serve` or `serve ls` lists, `serve rm <port>` removes, `serve whitelist <port> <pk-csv|clear|none>` edits the whitelist in place, `serve vhost` serves local sites on port 80 by Host header.
- Port 80 is the dmsghttp landing server. `--preserve-host` is for vhost backends. `--inject-pk` adds `X-Skywire-Remote-PK` for per key backend auth.

## Resolving SOCKS proxy
- `skywire cli resolver up` starts the dmsgweb SOCKS on `127.0.0.1:4445` and the skynetweb SOCKS on `127.0.0.1:4446`, chained dmsg to skynet to `--upstream`. Options are `--dmsg-only`, `--skynet-only`, `--no-chain`, `--bind`. `resolver down` stops it and bare `resolver` shows status.
- Fetch a served port with `curl -x socks5h://127.0.0.1:4445 http://<pk>.skynet:<port>/`.
- `127.0.0.1:4443` is a different proxy, bound under the survey whitelist key. It is for deployment and visor debug paths. See skywire-deployment-services.
- `resolver ca gen|install|uninstall|path|fingerprint` manages a local TLS CA limited to .skynet and .dmsg names. `install` and `uninstall` change the OS trust store and need root. Firefox keeps its own store.

## got and vpn
- `skywire cli got skynet://<pk>:<port>/path` fetches over the mesh. It also takes `dmsg://` and `http(s)://`.
- `vpn start [pk] --pk/-k <srv-pk>` has `--geoip <url>` (a probe URL fetched through the tunnel to check the exit IP, not a key), `--existing-tp`, `--local-route`, `--routing-policy` and `--verbose`. Also `vpn stop`, `status`, `list`, `ui`, `url`.
- `vpn server start|status|stop` takes `--whitelist/-w`, `--netifc/-i`, `--secure` and runs on Linux only.

## Traps
- Endless streams never finish. Measure with `curl --max-time N -w '%{size_download}'`, not `head -c | wc -c`.
- The server key is `--pk/-k` on proxy, skynet and vpn start, except `proxy test`.
