# One CXO feed per visor

Status: implemented on `perf/cxo-one-feed-per-visor`, off until the deployment
sets `feed_ingest`.

## Problem

A visor publishes a CXO feed for each deployment service it registers with.
Each feed is its own CXO node on its own dmsg port under the visor's key, and
each is announced to its service every 30 s.

| Port | Feed | Consumer | Publisher |
|---|---|---|---|
| 50 | telemetry shards and `tp-list` | TPD | `pkg/visor/init_stats.go:306` |
| 67 | dmsg discovery entry (`entry`) | dmsg discovery | `pkg/visor/init_registration_cxo.go:73` |
| 71 | AR binds (`stcpr`, `sudph`, `squicr`, `swtr`, `reach`) | address resolver | `pkg/visor/init_ar_bind_cxo.go:74` |
| 72 | SD entries (`services`) | service discovery | `pkg/visor/init_sd_reg_cxo.go:246` |

Every feed sends a heartbeat Root every 45 s on a quiet feed
(`pkg/cxo/treestore/publisher.go:957`). Each service aggregates its port with a
`cxoaggregate.Core` (`pkg/services/tpd/tpd.go:366`, `pkg/services/ar/ar.go:151`,
`pkg/services/sd/sd.go:164`, `pkg/services/dmsgdisc/dmsgdisc.go:312`). It
subscribes on the visor's announce conn and gets one OnRootFilled per Root.

prod01 runs all four services in one process. The 2026-10-16 audit counted
about 6.1k inbound CXO connections, 18.4k goroutines for them, 129 MB of conn
buffers and about 135 Roots/s, which cost 14 to 15% of the visor's CPU plus
8.7% in TPD's aggregator. Three of every visor's conns carry a few hundred bytes
that change only when a registration changes.

## Layout

The system feed (port 50) carries the registrations as top-level leaves next
to `tp-list`, each under its service's prefix (`pkg/cxo/sysfeed`).

```
tp-list                 transport list (unchanged)
transports/telemetry/…  telemetry shards (unchanged)
reg.dmsgd.entry         disc.Entry JSON, as on port 67
reg.ar.stcpr            addrresolver.LocalAddresses JSON, as on port 71
reg.ar.sudph
reg.ar.squicr
reg.ar.swtr
reg.ar.reach            arfeed.ReachDecl JSON
reg.sd.services         FrameGzip(v1, []servicedisc.Service), as on port 72
```

The bodies are byte for byte what the per-service feeds carry, so one decoder
per service reads both. The leaves sit in the Root's first child index, so
TPD's targeted fetch (`reconcileTargeted`) reads them from the same handful of
objects it already fetches for `tp-list`. The system feed keeps its allowlist
of TPD plus the peer whitelist, which is narrower than the open AR feed was.

## Services

Each regcxo package splits its Root handler into a `consume` function over a
leaf lookup. The aggregator on its own port calls it with the Root's leaves.
`NewFeedConsumer` wraps the same function for the system feed.

A service in TPD's process registers its consumer with
`cxoaggregate.RegisterFeedConsumer` (`services.ConsumeSystemFeed`). TPD's
aggregator holds a `cxoaggregate.Hub`. On every Root it receives (the targeted
fetch) and every Root it fills, including the host's own through `Ingest`, it
collects the `reg.` leaves and hands each service its own, with the prefix
stripped. Delivery is in-process, a function call with no dmsg stream, noise
handshake or CXO conn between the visor and that service.

A visor's feed publishes far more often than its registrations change, so the
hub delivers a service's leaves when they change and otherwise every 40 s
(`HubRefresh`). That keeps entry TTL refreshes at the old heartbeat rate. The
ingest paths were already idempotent and check the reporter (the Root's signer)
against the entry, so the source of a Root does not change what is accepted.

The legacy aggregators on 67, 71 and 72 keep running, so old visors are served
unchanged. `services.State` shows the hub's consumers and delivery count on
TPD's telemetry aggregator.

## Visors

`deployment.Services` gains `feed_ingest`, a list of the services that run in
TPD's process (`dmsg_discovery`, `address_resolver`, `service_discovery`). It
travels with the rest of the services config (embedded default, conf service
feed, hourly refresh, `deployment_services.json`).

At startup each registration module asks `systemFeedFor`. The registration
rides the system feed only when all of these hold.

1. The system feed runs (stats enabled and the publisher started).
2. The visor's TPD and the service are the ones in the deployment doc, so a
   visor pointed at its own SD keeps the SD feed.
3. The doc lists the service in `feed_ingest`.

Then the module writes its leaves through a prefixed sink on the system
publisher, starts no CXO node, and runs no announce loop. Its keepalive
predicate (`cxoKeepaliveHealthy`) watches TPD's subscription to the system feed
and the system feed's own announces, so HTTP re-registration is skipped while
TPD holds the feed and resumes the moment it does not. The epoch rule is the
same, so a TPD reconnect still triggers one HTTP registration (the SD needs it
for a visor's observed IP).

`visor state --jq '.cxo'` lists the riding services under `registrations` on
the `stats` feed.

## In-process dmsg sessions

The services on prod01 run under their own keys, each with its own dmsg
client, and those clients hold sessions with prod01's own dmsg server over QUIC
or TCP on loopback. A stream a remote visor opens to TPD through prod01's
server therefore crosses two sessions in one process.

The visor's dmsg server now calls `ServeInProcess` (`pkg/visor/init_dmsg.go`)
before it starts serving. A dmsg client in the same process that dials that
server gets a `net.Pipe` session (`pkg/dmsg/dmsg/server_inproc.go`), carrier
`inproc`. It keeps the Noise handshake and yamux, so the server handles it
like any accepted session, and it drops the QUIC crypto and the UDP syscalls
on both ends. Carrier convergence never moves a session off it. If the pipe
session fails the client dials the network as before. Since the server is
registered before it listens, a co-hosted client cannot have reached it over
the network first.

The remote path is unchanged. A visor elsewhere still reaches the services
through whichever dmsg server it shares with them.

## Transition

Old visors keep their per-service feeds whatever the deployment says. New
visors keep them until the deployment lists the service. New services accept
both. Nothing on the old path changes, so every mixed state works.

1. Merge. prod01 runs the new services, TPD delivers to consumers, and nothing
   uses that yet. Check `consumers` on TPD's telemetry aggregator in the
   embedded service state.
2. Set `"feed_ingest": ["dmsg_discovery", "address_resolver",
   "service_discovery"]` in the conf service's services config on prod01. The
   conf service must run this code first, or it drops the field. Visors read
   the field at startup, so develop visors switch on their next restart and apt
   boards when they take the release that carries this.
3. Watch the conn counts on the 67, 71 and 72 aggregators fall, the hub's
   delivery count rise, and SD, AR and dmsg discovery entry counts stay level.
   Rollback is removing the field, which takes effect as visors restart.
4. Remove the old path in the second release after the one that ships this,
   or 30 days after it, whichever is later, provided the legacy aggregators
   then hold under 2% of the fleet. That removes the visor's per-service
   publishers, the three regcxo aggregators' own nodes and ports 67, 71 and 72,
   the `feed_ingest` gate (the visor always uses the system feed), and the
   dedicated tp-list feed on 69, which is the same kind of leftover.

## Expected savings

Per visor the deployment host loses three CXO conns and keeps TPD's two (the
announce conn and TPD's dial-back). At about 950 visors that is about 2.8k of
prod01's 6.1k CXO conns, 8.5k goroutines, 60 MB of conn buffers and 63 of the
135 Roots/s, so roughly half of the CXO ingest cost, 7% of the visor's CPU. The
hub adds one scan of the Root's top-level children per received Root, over
objects the targeted fetch already holds.

Each visor runs one CXO node instead of four, and makes one announce dial
every 30 s instead of four.

The in-process sessions take the loopback QUIC out of every stream relayed
through prod01's own server to a co-hosted service, which covers all of the
remaining CXO conns that arrive through that server. The audit did not split
QUIC between relaying and loopback, so this part is not quantified.

These are estimates from the audit's figures. They were not measured on
production.
