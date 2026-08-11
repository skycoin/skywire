# Operator Guides

How-to documentation for skywire operators. Conceptual overview and
the latest install methods stay in the repo-root
[README](https://github.com/skycoin/skywire/blob/develop/README.md);
this directory holds the in-depth setup/usage guides.

For per-command help text (flags, usage, examples), see the generated
[command reference](../skywire/README.md) — `docs/skywire/` mirrors the
cobra subcommand tree.

## Setup

- [install.md](install.md) — install via `go install` / `go run`, Linux packages, Docker, NixOS, release binaries
- [permissions.md](permissions.md) — VPN client CAP_NET_ADMIN, VPN server iptables/sysctl, file system + survey
- [configuration.md](configuration.md) — `config gen` flags, hypervisor web UI, hypervisor TUI, remote hypervisors, network visualization UI
- [visor.md](visor.md) — running `skywire visor`, process control / while-loop pattern, transport setup, runtime files
- [public-visor.md](public-visor.md) — make a visor reachable from the internet: `is_public`, a stable `transport_port`, router port-forwarding and host firewall (TCP **and** UDP), transport-type → protocol map, and reachability troubleshooting

## Application usage

- [vpn.md](vpn.md) — Skywire VPN client/server with `skywire cli vpn`
- [socks5.md](socks5.md) — Skywire SOCKS5 proxy client with `skywire cli proxy`
- [skynet.md](skynet.md) — SkyNet P2P port forwarding with `skywire cli skynet`
- [resolving-proxy.md](resolving-proxy.md) — the `.dmsg` / `.skynet` resolving SOCKS5 proxies (`skywire cli resolver`): reach visors and deployment services by public key over dmsg/skynet from a browser or `curl` — the supported way to view deployment-service data now that the CLI has no plain-HTTP fallback
- [dmsg-lan-gateway.md](dmsg-lan-gateway.md) — turn one board with a visor into a `.dmsg` / `.skynet` gateway for your whole LAN (OpenWRT / DD-WRT), so any device reaches dmsg-only deployment services with no per-device skywire install (via `skywire autoconfig --dmsgweb-addr`)
- [standalone-skychat.md](standalone-skychat.md) — `skywire app skychat --standalone --tcp-listen :PORT` for visor-independent chat-app with direct-TCP entry point; port-forwarding caveats
- [standalone-dmsgpty-host.md](standalone-dmsgpty-host.md) — `skywire dmsg pty host --tcplisten :PORT` for visor-independent pty server with DMSG + TCP modes; port-forwarding caveats

## Advanced

- [remote-visor-cli.md](remote-visor-cli.md) — drive a visor on another machine with `skywire cli --via`: the trust model (`hypervisors` / dmsgpty whitelist), hypervisor attach vs the full CLI bridge, the two-terminal remote-diagnosis session, and what of it works against the Android app's visor
- [manual-routing.md](manual-routing.md) — manual route creation, multi-hop routes, route-finder, troubleshooting
- [privacy-and-performance.md](privacy-and-performance.md) — tuning the visor along the privacy ↔ performance spectrum: IP/data/metadata privacy, `min_hops`, multiplexed & rotating routes, `ar_transport_limit`, `no_direct_transports`, `persistent_transports`, and a maximum-privacy recipe

## Maintainer

- [testing.md](testing.md) — pre-PR `make format check` workflow
- [release.md](release.md) — creating a GitHub release with goreleaser
