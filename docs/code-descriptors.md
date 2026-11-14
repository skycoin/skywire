# Source-file descriptors: `c<layer>-<domain>-<system>`

Every hand-written Go file in this repository opens with a package comment
carrying a short code:

```go
// Package visor pkg/visor/init_dmsg.go c3-vis-core
// Package dmsg pkg/dmsg/dmsg/client_dial.go c1-net-dmsg
// Package deployment deployment/config.go c0-com-env
```

That trailing code is a **taxonomy label**. It was applied across the tree
without being written down anywhere in the repo, which has repeatedly caught out
contributors — you cannot apply a standard to a new file if nothing says what
the standard is. This document is that missing piece.

It is derived from the KCG Go standards adaptation
([`docs/sss3/domains-golang.txt`](sss3/domains-golang.txt), v1.2, 2026-07-07),
which itself adapts a larger C#/Unity house style to Go under
one governing rule: **idiomatic Go beats transliterated C#.** Where the original
standard and Go idiom conflict, Go wins and the conflict is recorded rather than
patched into the code.

## What the three parts mean

```
c<layer>-<domain>-<system>
   │        │        └── the subsystem inside the domain
   │        └─────────── one of exactly four: com | net | app | vis
   └──────────────────── c0..c4, the dependency tier
```

**`c<layer>` — the dependency tier.** `c0` is the most generic; a lower tier
never imports a higher one. This is the one part that is a hard fact rather than
a label: Go's compiler rejects import cycles, so the layering is enforced by
`go build` and cannot silently rot.

**`<domain>` — one of four.** Deliberately few. The guidance was *"1 to 3
domains is enough — use fewer domains and push the detail into SYSTEMS; only
make a domain if a thing can't be a system."*

| domain | what it holds |
|---|---|
| `com` | cross-cutting `c0` primitives; imported by everyone, imports nothing internal |
| `net` | the mesh: transports, routing, object-sync, discovery services |
| `app` | what runs *on* the mesh: user apps and the reward/viz surfaces |
| `vis` | the visor platform that assembles net+app into a running peer, plus control surfaces |

**`<system>` — the subsystem.** This is where detail lives, and where most new
labels should go. Earlier drafts had eight domains; `rte`, `cxo`, `dsc` and
`rwd` were demoted to systems (`net.routing`, `net.cxo`, `net.discovery`,
`app.rewards`) precisely because a system is the cheaper unit.

## The systems

### `com` — c0 foundation

| system | packages |
|---|---|
| `com.crypto` | `cipher` |
| `com.log` | `logging` |
| `com.http` | `httputil`, `httpauth`, `httpauthclient`, `gobrpc` |
| `com.env` | `skyenv` — shared constants only |
| `com.util` | `util`, `netutil`, `cmdutil`, `flags`, `metricsutil`, `buildinfo`, `geo`, `geoip`, `pg`, `testhelpers`, `fastjson`, `gcmon`, `flightrec`, `cliout` and `cliout/clihelp` |

Three of these are not c0. `cmdutil` and `flags` are c1 because they import
the dmsg client, and `httpauth` is c2 because it imports `cxo/storeconfig`.
`skywireconfig/skyenvfile` is `c0-com-env`, since it imports nothing.

### `net` — the mesh

| system | packages |
|---|---|
| `net.dmsg` | `dmsg` (c1) — the substrate; `dmsgc` (c2), which imports `app/appevent` |
| `net.transport` | `transport`, `transport-setup`, `skyquic`, `skyudpbridge`, `stunserver`, `tcpproxy` (c1/c2) |
| `net.routing` | `routing`, `router`, `rfclient`, `route-finder`, `skyroute` (c1/c2) |
| `net.cxo` | `cxo` (node, treestore, skyobject, cxosub), `storeconfig` (c2) |
| `net.discovery` | UT, `serviceconfig`, config-bootstrapper (c4); the TPD, AR, SD, RF and conf backends, `deployment/charts`, `deployment/netgraph`, `cxo/cxoaggregate` and `dmsg/discovery/api`, which the visor embeds (c3); the clients `servicedisc`, `utclient`, `serviceuptime`, `uptimestats`, `dmsg/discovery/serverfeed` (c2) |
| `net.monitor` | `networkmonitor`, `network-monitor` (c2) |

### `app` — what runs on the mesh

| system | packages |
|---|---|
| `app.proxy` | `skysocks` (+ client) (c4); `proxyenv`, `proxyfront`, `proxyroute`, `skysocks/skysettings` (c2) |
| `app.chat` | `skychat` |
| `app.vpn` | `vpn` (client/server), `skydns` |
| `app.skynet` | `skynet`, `skynetweb`, `skynetca` |
| `app.web` | `dmsgweb`, `proxyinterstitial` (c3); `proxystatus` (c2) |
| `app.mail` | `skymail`, `skymailbridge` |
| `app.rewards` | `rewards` (with the reward system server `deployment/rewards/server`), `tpviz` (with `netview`, `wasmgl`, `cmd/netview`), `visnetwork` |
| `app.wallet` | `wallet`, `btcgateway`, `skycoin-web`, `cmd/skycoin`, skydex client and market |

### `vis` — the visor platform

| system | packages |
|---|---|
| `vis.core` | `visor` (with `visorapi`), `skywireconfig`/`visorconfig`, `svcmode`, `alerts` (c3) |
| `vis.appsvc` | `app` (the app-running framework / appserver) (c2); `services` (c3) |
| `vis.wasm` | `wasmhv` (with `deskhost`, `execwasm`), `wasmrpc` (c3) |
| `vis.pty` | `pty` (c3) |
| `vis.cli` | `cmd/skywire`, `cmd/skywire-cli`, `cmd/skywire-visor`, `cmd/hvinspect`, `clicache`, the `cliout/cli*` output shapes, `internal/rpcgen` (c3/c4) |

### Choices for packages that carried more than one descriptor

One package, one descriptor. Where files of one package disagreed, the package
took the descriptor of what it is as a whole. The non-obvious ones:

| package | descriptor | why |
|---|---|---|
| `pkg/dmsg/discovery/api` | `c3-net-discovery` | it is the dmsg discovery server, a deployment backend that imports `cxo/treestore` and `deployment/charts`, so it cannot be `c1`, and the visor embeds it |
| `pkg/dmsgweb` | `c3-app-web` | one file dialing over dmsg does not make the package part of the substrate |
| `pkg/proxystatus` | `c2-app-web` | it renders proxy status pages, and `pkg/app` (c2) imports it |
| `pkg/routing` | `c1-net-routing` | it imports only `cipher` and `logging` |
| `pkg/services` | `c3-vis-appsvc` | the embedded service runner, which imports `svcmode` and the c3 backends; `keyfile.go` is part of it, not of discovery |
| `pkg/transport` | `c2-net-transport` | `c1-net-tp` and `c4-net-transport` were stray labels on single files |
| `pkg/tpviz` | `c3-app-rewards` | `vis.latency` was never a system; `netview` and `wasmgl` follow their parent |
| `pkg/visor`, `pkg/visor/visorapi` | `c3-vis-core` | `vis.api` and `vis.browse` were never systems, and the hostbridge and pty files are part of the visor |
| `pkg/visor/visorconfig` | `c3-vis-core` | the wallet section is part of the visor config; config rides with what it configures |
| `pkg/app/appnet`, `pkg/app/launcher` | `c2-vis-appsvc` | the app framework |
| `pkg/vpn` | `c3-app-vpn` | the embed and js client files are part of the app |
| `cmd/skywire-cli/commands/proxy` | `c4-vis-cli` | a CLI command group, whatever it drives |

The others (`pkg/cxo/cxosub`, `pkg/httputil`, `pkg/logging`, `pkg/pty`,
`pkg/router`) kept the descriptor most of their files already had.

## Where config goes

Config is **never** its own catch-all system. A config package is tagged with
the domain/system of *the thing it configures* — it sits next to its subject.

| package | configures | tag |
|---|---|---|
| `skyenv` | everyone (constants) | `c0-com-env` |
| `skywireconfig` / `visorconfig` | the visor | `c3-vis-core` |
| `svcmode` | visor service mode | `c3-vis-core` |
| `clicache` | the CLI's RPC address | `c4-vis-cli` |
| `serviceconfig` | deployment services | `c4-net-discovery` |
| `storeconfig` | a service's backing store | `c2-net-cxo` / `c4-net-discovery` |

`skyenv` is the single exception: it configures nothing in particular, it is the
shared vocabulary every domain reads.

The point is that `grep c3-vis-core` shows the visor *and* its config together,
and no "cfg" system pretends unrelated apps share configuration.

## What this is NOT

**It is not a rename.** The layer, domain and system are labels you read and
grep. They are never part of an identifier, a package name or an import path.

**Domain and system must not become a lint rule.** They are a documentation
grouping with no Go analogue — an `app` package importing `net` is entirely
normal. Only the *layer* corresponds to something the compiler checks, and it
checks it already.

So `make check-descriptors` deliberately verifies only two things:

1. every non-test `.go` file carries a well-formed descriptor, and
2. all files in one package agree on it.

It does not, and should not, decide which domain a package belongs to.
Generated files (those with the Go `// Code generated ... DO NOT EDIT.` header,
such as the templ output in `pkg/visor/ui2` and the mocks) and `testdata` are
skipped, since a tag there would be lost on the next generate. The check runs
in `make check` and in CI through `make check-ci`.

## Adding a file

Copy the descriptor from the file next to it. If you are creating a new package,
find its system in the tables above and pick the layer from what it imports —
if it imports `visor`, it is not `c0`.

The header convention is the package clause, the path, then the descriptor:

```go
// Package proxystatus pkg/proxystatus/routetree.go c2-app-web
```

Where a file already has prose package documentation, the descriptor line goes
first and a bare `//` line separates it from the prose.

## Known drift

None that the check can see. As of 2026-11-12 every hand-written non-test file
carries a descriptor and every package carries exactly one, so
`scripts/check-descriptors.sh` has no allow list and fails on any new drift.

What the check cannot see is the layer. The compiler forbids cycles but not a
lower tier label importing a higher one. As of 2026-11-14 no package does
that. The visor-launched apps and the deployment backends the visor embeds
are c3, the layer they are used at, and the reward system server lives in
`pkg/deployment/rewards/server` rather than under `cmd/`. When a new import
points upward, retag the package to the layer of what it imports, or move
the code, rather than leaving the label wrong.
