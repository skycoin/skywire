# Source-file descriptors: `c<layer>-<domain>-<system>`

Nearly every Go file in this repository opens with a package comment carrying a
short code:

```go
// Package visor pkg/visor/init_dmsg.go c3-vis-core
// Package dmsg pkg/dmsg/dmsg/client_dial.go c1-net-dmsg
// Package deployment deployment/config.go c0-com-env
```

That trailing code is a **taxonomy label**. It was applied across the tree
without being written down anywhere in the repo, which has repeatedly caught out
contributors — you cannot apply a standard to a new file if nothing says what
the standard is. This document is that missing piece.

It is derived from the KCG Go standards adaptation (`domains-golang.txt`,
v1.2, 2026-07-07), which itself adapts a larger C#/Unity house style to Go under
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
| `com.util` | `util`, `netutil`, `cmdutil`, `flags`, `metricsutil`, `buildinfo`, `geo`, `geoip`, `pg`, `testhelpers` |

### `net` — the mesh

| system | packages |
|---|---|
| `net.dmsg` | `dmsg`, `dmsgc` (c1) — the substrate |
| `net.transport` | `transport`, `transport-setup`, `skyquic`, `skyudpbridge`, `stunserver`, `tcpproxy` (c1/c2) |
| `net.routing` | `routing`, `router`, `rfclient`, `route-finder`, `skyroute` (c1/c2) |
| `net.cxo` | `cxo` (node, treestore, skyobject, cxosub), `storeconfig` (c2) |
| `net.discovery` | TPD, AR, SD, UT, `servicedisc`, `utclient`, `serviceuptime`, `uptimestats`, `serviceconfig`, config-bootstrapper (c2/c4) |
| `net.monitor` | `networkmonitor`, `network-monitor` (c2) |

### `app` — what runs on the mesh

| system | packages |
|---|---|
| `app.proxy` | `skysocks` (+ client) |
| `app.chat` | `skychat` |
| `app.vpn` | `vpn` (client/server) |
| `app.skynet` | `skynet`, `skynetweb`, `skynetca` |
| `app.web` | `dmsgweb` |
| `app.mail` | `skymailbridge` |
| `app.rewards` | `rewards`, `tpviz`, `visnetwork` |

### `vis` — the visor platform

| system | packages |
|---|---|
| `vis.core` | `visor`, `skywireconfig`/`visorconfig`, `svcmode` (c3) |
| `vis.appsvc` | `app` (the app-running framework / appserver), `services` (c1/c2) |
| `vis.wasm` | `wasmhv`, `wasmrpc` (c3) |
| `vis.pty` | `pty` (c3) |
| `vis.cli` | `cmd/skywire`, `cmd/skywire-cli`, `cmd/skywire-visor`, `cmd/hvinspect`, `clicache` (c3/c4) |

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

## Adding a file

Copy the descriptor from the file next to it. If you are creating a new package,
find its system in the tables above and pick the layer from what it imports —
if it imports `visor`, it is not `c0`.

The header convention is the package clause, the path, then the descriptor:

```go
// Package proxystatus pkg/proxystatus/routetree.go c3-vis-core
```

## Known drift

As of 2026-09-13: 96 of ~1580 non-test files carry no descriptor, and 12
packages carry more than one. Both are recorded in
`scripts/check-descriptors.sh` so the check fails on *new* drift while the
existing debt is visible and countable rather than silently tolerated. Fixing an
entry means removing it from that list in the same commit.
