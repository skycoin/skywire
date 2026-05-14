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

## Application usage

- [vpn.md](vpn.md) — Skywire VPN client/server with `skywire cli vpn`
- [socks5.md](socks5.md) — Skywire SOCKS5 proxy client with `skywire cli proxy`
- [skynet.md](skynet.md) — SkyNet P2P port forwarding with `skywire cli skynet`

## Advanced

- [manual-routing.md](manual-routing.md) — manual route creation, multi-hop routes, route-finder, troubleshooting

## Maintainer

- [testing.md](testing.md) — pre-PR `make format check` workflow
- [release.md](release.md) — creating a GitHub release with goreleaser
