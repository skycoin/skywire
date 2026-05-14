[![Go Report Card](https://goreportcard.com/badge/github.com/skycoin/skywire)](https://goreportcard.com/report/github.com/skycoin/skywire)
![Test](https://github.com/skycoin/skywire/actions/workflows/test.yml/badge.svg)
![Deploy](https://github.com/skycoin/skywire/actions/workflows/deploy.yml/badge.svg)
[![GitHub release](https://img.shields.io/github/release/skycoin/skywire.svg)](https://github.com/skycoin/skywire/releases/)
[![skywire](https://img.shields.io/aur/version/skywire?color=1793d1&label=skywire&logo=arch-linux)](https://aur.archlinux.org/packages/skywire/)
[![skywire-bin](https://img.shields.io/aur/version/skywire-bin?color=1793d1&label=skywire-bin&logo=arch-linux)](https://aur.archlinux.org/packages/skywire-bin/)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/skycoin/skywire/badge)](https://api.securityscorecards.dev/projects/github.com/skycoin/skywire)
[![go.mod](https://img.shields.io/github/go-mod/go-version/skycoin/skywire.svg)](https://github.com/skycoin/skywire)
[![Telegram](https://img.shields.io/badge/Join-Telegram-blue?&logo=data:image/svg%2bxml;base64,PHN2ZyBlbmFibGUtYmFja2dyb3VuZD0ibmV3IDAgMCAyNCAyNCIgaGVpZ2h0PSI1MTIiIHZpZXdCb3g9IjAgMCAyNCAyNCIgd2lkdGg9IjUxMiIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj48cGF0aCBkPSJtOS40MTcgMTUuMTgxLS4zOTcgNS41ODRjLjU2OCAwIC44MTQtLjI0NCAxLjEwOS0uNTM3bDIuNjYzLTIuNTQ1IDUuNTE4IDQuMDQxYzEuMDEyLjU2NCAxLjcyNS4yNjcgMS45OTgtLjkzMWwzLjYyMi0xNi45NzIuMDAxLS4wMDFjLjMyMS0xLjQ5Ni0uNTQxLTIuMDgxLTEuNTI3LTEuNzE0bC0yMS4yOSA4LjE1MWMtMS40NTMuNTY0LTEuNDMxIDEuMzc0LS4yNDcgMS43NDFsNS40NDMgMS42OTMgMTIuNjQzLTcuOTExYy41OTUtLjM5NCAxLjEzNi0uMTc2LjY5MS4yMTh6IiBmaWxsPSIjMDM5YmU1Ii8+PC9zdmc+)](https://t.me/skywire)

**PLEASE ALWAYS USE THE [DEVELOP BRANCH](https://github.com/skycoin/skywire/tree/develop)**

# Skywire

Skywire is a fully open-source, privacy-focused suite of networking
tools developed by Skycoin. The public Skywire Network enables this
software to be developed and tested in real-world conditions, with
[daily rewards in Skycoin](rewards/mainnet_rules.md) ($SKY) distributed
to eligible participants.

## Major features

Skywire visors are reachable over two distinct encrypted networks,
both addressed by 33-byte public keys:

- **Skywire** — a peer-to-peer routing network. Visors establish
  encrypted transports directly with each other (STCPR over TCP, SUDPH
  over UDP hole-punching) and build single-hop or multi-hop routes
  across them using the Noise Protocol; intermediate visors see only
  the previous and next hop.
- **DMSG** — a relay-based messaging network. Visors connect as
  clients to DMSG servers, which relay encrypted streams between them
  on the clients' behalf; the two endpoints never need direct
  connectivity to each other.

The two networks share the same pubkey identity space and can be used
independently or together — every feature below works over either.

* **P2P port forwarding over Skywire and DMSG** — host websites and TCP services on your visor's public key.
  -- [SkyNet](#skynet--p2p-port-forwarding-over-skywire) forwards over Skywire routes.
  -- [DmsgWeb](#dmsgweb--anonymous-port-forwarding-over-dmsg) forwards over a DMSG relay.
* **`.skynet` / `.dmsg` resolving SOCKS5 proxy** — point a browser at the visor's local resolver to reach `<pk>.skynet` and `<pk>.dmsg` URLs directly.
  -- Subdomain prefix on the URL (`example.com.<pk>.skynet`) lets vhost-capable backends like Caddy / nginx dispatch by `Host` header through the visor's port forwarder.
  -- Optional TLS-MITM mode mints leaf certs from a locally-installed name-constrained CA so HTTPS sites work in the browser without warnings.
* **Direct, multi-hop, and multiplexed pubkey-encrypted routing** — NAT-traversing transports plus DMSG fallback mean no public IP is required.
  -- STCPR (TCP relay) and SUDPH (UDP hole-punching) auto-create transports between visors.
  -- Routes use the Noise Protocol (ChaCha20-Poly1305) end-to-end; intermediate visors only know the previous and next hop.
  -- Multi-route mux groups multiple parallel routes between the same endpoints for higher bandwidth.
* **Native apps** — managed by the visor and registered into service discovery.
  -- VPN client and server.
  -- SOCKS5 proxy client and server (skysocks / skysocks-client).
  -- skychat messenger with persistent chat history via CXO + bbolt — messages survive restarts.
* **Remote terminal, monitoring, and management over DMSG / SkyNet** — access any visor's terminal, runtime logs, and live stats from anywhere.
  -- `skywire cli` over DMSG / SkyNet for scripting and one-shot commands.
  -- Hypervisor browser UI for clusters; everything tunnels over the same pubkey-authenticated transports.
* **Custom / corporate / private network deployments** — run your own service stack (transport discovery, route finder, service discovery, address resolver, etc.) using [skywire-deployment](https://github.com/skycoin/skywire-deployment), or layer additional deployments on top of the public network for segmented environments.
* **Decentralized standalone operation** — hypervisor-embedded DMSG server lets a Skywire network keep running without an active connection to the public deployment after the initial config and bootstrap; useful for air-gapped, LAN-only, or self-hosted networks.

## Skywire Control and Data Planes

[Skywire](https://skycoin.com/skywire) uses [dmsg](https://github.com/skycoin/dmsg)
as a control plane to enable all Skywire visors to connect to each
other and to deployment services provided by the public
[Skywire Network](https://conf.skywire.skycoin.com) (or a user-hosted
deployment). DMSG (Read as: `D-message`) functions as a simple relay
system and **encrypted** transport implementation, facilitating
anonymous connections between dmsg clients (i.e., encrypted pubkey-
based automatic routing), mediated by the dmsg server. Skywire expands
upon this by creating a data plane of direct, secure, encrypted peer-
to-peer transports between visors, which may then be used for routes.

## Skywire Network and Transports

A Skywire visor is identified by its public key. Skywire transports
are encrypted via the public keys of the visors on each side of the
transport. Skywire uses a whitelist system to enable trusted nodes
(route setup nodes) to set up routes as calculated by the route finder
service through established
[transports registered in the transport discovery](https://tpd.skywire.skycoin.com/all-transports).
An [automatic transport creation mechanism](pkg/visor/autoconnect.go),
enabled by default, is used to establish transports to
[public visors](https://sd.skycoin.com/api/services?type=visor) via
STCPR (Skywire TCP Relay) transports, and to visors connected to
public visors via SUDPH (Skywire UDP Hole-punching) transports. This
auto-transport mechanism is designed to create adequate transports for
multi-hop routing.

## Skywire Routing

Skywire routes consist of one or more transports. A Skywire route may
not transit the same public key twice, in order to prevent data loops.
The Skywire routing system is designed with privacy in mind to defeat
data snooping efforts. Packets are encrypted using the Noise Protocol
(ChaCha20-Poly1305), making their contents appear as random data to
observers. A visor handling transports where data flows is only aware
of the public key of the previous hop and the next hop — not the
ultimate source or destination of the packet. These measures
significantly mitigate the risk of metadata leakage or traffic
analysis. When a transport is trafficking data from multiple sources
and destinations, it becomes difficult to perform traffic correlation
attacks or related exploits. Another planned feature is route
multiplexing, which will multiplex multi-hop routes and permit more
bandwidth between the source and destination — similar in concept to
BitTorrent.

## Skywire Visor

The name 'visor' was chosen as a less ambiguous term than 'node' to
refer to the running Skywire process. The term 'node' is typically
reserved as a reference to the hardware on which Skywire is running,
in this ecosystem. A Skywire visor participates in transports and
provides an interface to applications which can be accessed over or
consume routes. The Skywire visor can also be configured to provide a
hypervisor web UI for remotely managing a cluster of Skywire visors /
nodes, typically referred to as a
[skyminer](https://www.skycoin.com/skyminer/).

For running and configuring a visor see
[docs/guides/visor.md](docs/guides/visor.md) and
[docs/guides/configuration.md](docs/guides/configuration.md).

## Skywire Cli (command line interface)

`skywire cli` is the primary interface to a running Skywire visor.
Skywire cli provides an interface to generate a JSON config file for
the Skywire visor, to control visor native applications, and to access
data from different Skywire services.

Full reference: [docs/skywire/cli/](docs/skywire/cli/README.md).

## Skywire Apps

Skywire visors include native VPN and SOCKS5 proxy server and client
applications, as well as a messenger application, which are started
and managed by the visor. When a server application is started, it
registers itself in the service discovery as a
[proxy server](https://sd.skycoin.com/api/services?type=proxy) or
[VPN server](https://sd.skycoin.com/api/services?type=proxy). These
services may then be consumed by respective client applications via
either a direct or multi-hop route.

Operator guides: [vpn](docs/guides/vpn.md), [socks5](docs/guides/socks5.md), [skynet](docs/guides/skynet.md).

## DmsgWeb – Anonymous port forwarding over DMSG

The `skywire dmsg web` and `skywire dmsg web srv` subcommands allow
port forwarding over DMSG. Additionally, DmsgWeb provides a resolving
SOCKS5 proxy, similar to and inspired by I2P, which permits convenient
configuration of a web browser to access DMSG websites. With
additional proxy configuration, all browser traffic can be routed
through a Skywire SOCKS5 proxy connection. With Skywire's advanced
routing, the already anonymous DMSG utilities can be made even more
private by routing them through a Skywire SOCKS5 proxy connection.

## SkyNet – P2P port forwarding over Skywire

SkyNet is the Skywire counterpart to DmsgWeb — facilitating port
forwarding over Skywire's peer-to-peer transport types and advanced
routing, without transiting a DMSG server. With SkyNet, you can:

* **Expose local ports**: Run a SkyNet server to make local TCP services accessible to other Skywire visors
* **Connect to remote services**: Use the SkyNet client to forward remote ports to your localhost
* **Access control**: Whitelist specific public keys to restrict who can connect to your server
* **Multiple instances**: Run multiple server and client instances simultaneously with unique names

Operator usage: [docs/guides/skynet.md](docs/guides/skynet.md).

## Skywire Deployment Services

Skywire enables users to create their own network if desired. The
implementation is fully open source.
[Documentation for making a custom Skywire deployment is here.](https://github.com/skycoin/skywire-deployment)

## Skywire Rewards

The [Skywire reward system](https://fiber.skywire.dev) is the
distribution mechanism for [Skycoin](https://skycoin.com). Skycoin is
not 'mined' as with other cryptocurrencies; rewards in Skycoin ($SKY)
are distributed daily to eligible Skywire visors who meet the
[requirements for obtaining rewards](rewards/mainnet_rules.md).

Despite the terminology, Skywire visors do not process Skycoin
transactions. Skywire visors do not sync the Skycoin blockchain and
have no involvement in transaction processing. The only relationship
between skywire and the skycoin cryptocurrency is via the reward
system acting as the distribution mechanism for Skycoin.

Set a reward address:
```
skywire cli reward <skycoin-address>
```
Visors meeting uptime and eligibility requirements will receive daily
skycoin rewards for up to 8 visors per location / IP address. Only
package-based linux installations are currently supported for rewards.

## Documentation

Command-line reference, generated from the live cobra tree:

* [docs/skywire/](docs/skywire/README.md) — every command's `--help`,
  one markdown page per command, mirroring the subcommand hierarchy.
  Run `skywire doc` (or `make doc-gen`) from the repo root to
  regenerate after CLI changes.

Operator how-to guides:

* [docs/guides/install.md](docs/guides/install.md) — install via package, release binary, Docker, Nix, or `go install`
* [docs/guides/permissions.md](docs/guides/permissions.md) — VPN capabilities, sudoers, system survey
* [docs/guides/configuration.md](docs/guides/configuration.md) — `config gen`, hypervisor UI, network visualization
* [docs/guides/visor.md](docs/guides/visor.md) — run / supervise `skywire visor`, transports, runtime files
* [docs/guides/vpn.md](docs/guides/vpn.md) — Skywire VPN
* [docs/guides/socks5.md](docs/guides/socks5.md) — Skywire SOCKS5 proxy
* [docs/guides/skynet.md](docs/guides/skynet.md) — SkyNet port forwarding
* [docs/guides/manual-routing.md](docs/guides/manual-routing.md) — manual route creation, multi-hop, route-finder
* [docs/guides/testing.md](docs/guides/testing.md) — pre-PR `make format check`
* [docs/guides/release.md](docs/guides/release.md) — creating a GitHub release

Visor native applications:

* [API](docs/skywire_app_api.md)
* [skychat](cmd/apps/skychat/README.md)
* [skysocks](cmd/apps/skysocks/README.md) / [skysocks-client](cmd/apps/skysocks-client/README.md)
* [vpn-client](cmd/apps/vpn-client/README.md) / [vpn-server](cmd/apps/vpn-server/README.md)
* [skynet](cmd/apps/skynet/README.md) / [skynet-client](cmd/apps/skynet-client/README.md)

Example custom applications:

* [example-server-app](example/example-server-app/README.md)
* [example-client-app](example/example-client-app/README.md)

Further docs: [skywire wiki](https://github.com/skycoin/skywire/wiki).

## Dependencies

### Build Deps

* `golang` — install with your system package manager on most linux
  distributions, or follow [go.dev/doc/install](https://go.dev/doc/install).
  Basic setup of the `go` environment is further described
  [here](https://github.com/skycoin/skycoin/blob/develop/INSTALLATION.md#setup-your-gopath).
* `git` (optional)
* `musl` and `kernel-headers-musl` or equivalent — for static
  compilation; see [docs/static-builds.md](docs/static-builds.md).

### Visor Runtime Deps

* `glibc` or `libc6` — unless statically compiled.

### Testing Deps

* `golangci-lint`
* `goimports-reviser` from github.com/incu6us/goimports-reviser/v2
* `goimports` from golang.org/x/tools/cmd/goimports

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
go run github.com/loov/goda@latest graph github.com/skycoin/skywire/... | dot -Tsvg -o docs/skywire-goda-graph.svg
```

![Dependency Graph](docs/skywire-goda-graph.svg "github.com/skycoin/skywire Dependency Graph")
