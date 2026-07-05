# Reward server (tpviz) → CXO subscriber for all deployment feeds

## Problem

The standalone reward server (`skywire cli rewards server`, serving
theskywirenetwork.net over dmsg) fetches TPD/SD/DMSGD data via **HTTP-over-dmsg
polling** (`tpviz.SetDmsgHTTPClient`). The deployment services are dmsg-only and
already publish their data on **CXO feeds**, so the right mechanism is an
**intermittent CXO subscriber** (subscribe → wait-for-Root → snapshot →
unsubscribe), which also handles resubscribe and doesn't require the publisher
(TPD) to persistently track subscribers across restarts.

The concrete trigger: `/api/uptimes` was empty (standalone UT decommissioned) →
all visors "unknown" → hidden → transport-graph showed ~9 of ~1011 visors.
PR #3388 patched it via HTTP-over-dmsg; this doc is the proper CXO version.

## What already exists

- TPD/SD/DMSGD CXO publishers, consumed today by the **visor**'s HV visualizer:
  | Feed | Publisher | dmsg port (skyenv) | prefix | payload |
  |---|---|---|---|---|
  | `FeedTPDMetrics` | TPD | `DmsgTPDMetricsCXOPort` (51) | `metrics/days/` | `[]TransportMetric` |
  | `FeedTPDUptime` | TPD | `DmsgTPDUptimeCXOPort` (52) | `uptimes/days/` | `[]VisorSummary` |
  | `FeedSDServices` | SD | `DmsgSDServicesCXOPort` (53) | `services/` | services entries |
  | `FeedDMSGDClientsByServer` | DMSG-D | `DmsgDMSGDClientsByServerCXOPort` | `clients-by-server/` | client entries |
  | `FeedTPDAllTransports` | TPD | `DmsgTPDAllTransportsCXOPort` | `transports/all/` | all-transports snapshot |
- `pkg/visor/cxo_subscription_manager.go` (800 lines) implements the intermittent
  subscriber: `syncOnce` (subscribe/snapshot/unsubscribe), `AcquireForTab` /
  `ReleaseForTab` grace batching, `Get`/`Walk`, `feedSpec` (feed → pk/port/prefix
  from `v.conf`). It imports only `cipher`, `cxo/treestore`, `dmsg/dmsgcurl`,
  `logging`, `skyenv` — **no visor-specific packages**.
- `pkg/tpviz/cxo_subscriber.go` defines the `CXOSubMgr` interface
  (`AcquireForTab`/`ReleaseForTab`/`Walk`) tpviz consumes, plus `tryCXOServices`
  and `tryCXOClientsByServer` (services + dmsg already CXO-capable when a mgr is
  wired). Only the **hypervisor** wires one (`hypervisor.go:145`,
  `cxoSubMgrAdapter{v: visor}`); the standalone reward server wires none.

## Recommendation: extract the manager to a lightweight package

**Approach A**, but as a clean *move* into a new package rather than importing the
heavy `pkg/visor`:

1. **Move** `cxo_subscription_manager.go` → new package **`pkg/cxo/cxosub`**
   (`manager.go`). Because it already imports nothing visor-specific, this is a
   near-verbatim move.
2. **Replace the `*Visor` coupling** with an injected dependency set:
   ```go
   package cxosub
   type Feed int // the CXOFeed constants move here
   type Deps struct {
       Dmsg     *dmsgcurl.Client                  // was v.dmsgC
       FeedSpec func(Feed) (pk cipher.PubKey, port uint16, prefix string, err error) // was m.feedSpec
       Log      *logging.Logger                   // was v.MasterLogger
   }
   func NewManager(deps Deps, interval time.Duration) *Manager
   // Manager keeps AcquireForTab/ReleaseForTab/Get/Walk unchanged.
   ```
   `initLock` / `cxoSubMgr` self-ref become manager-internal.
3. **`pkg/visor`**: a thin shim builds `cxosub.Deps` from `v.conf`
   (`tpdCXOPeer/sdCXOPeer/dmsgdCXOPeer` → `FeedSpec`) + `v.dmsgC`. The existing
   `cxoSubMgrAdapter` continues bridging to tpviz's `CXOSubMgr`. Behavior
   unchanged; the visor keeps its tab→feed map.
4. **Reward server** (`cmd/skywire-cli/commands/rewards/server`): build
   `cxosub.Deps` from **deployment config dmsg PKs**
   (`deployment.Prod.TransportDiscoveryDmsg` → TPD PK, `…ServiceDiscoveryDmsg`,
   `…DmsgDiscoveryDmsg`) + the reward server's existing embedded dmsg client
   (`StartDmsgEmbedded`), construct `cxosub.NewManager`, wrap in a small adapter
   implementing tpviz's `CXOSubMgr`, and call `tpvizServer.SetCXOSubMgr(...)`.
   This replaces `SetDmsgHTTPClient` as the primary path.
5. **`pkg/tpviz`**: add the missing feed constants (`CXOFeedTPDMetrics=0`,
   `CXOFeedTPDUptime=1`, `CXOFeedTPDAllTransports=4`; services=2, cbs=3 exist) and
   `tryCXOUptimes` (Walk `uptimes/days/`), `tryCXOTransports` (Walk
   `transports/all/`), `tryCXOMetrics` (Walk `metrics/days/`). Wire each handler
   CXO-first with the HTTP-over-dmsg path as fallback; once validated, retire the
   HTTP polling in the reward server.

**Feed coverage:** all five (metrics, uptime, services, clients-by-server,
all-transports) — the complete dmsg-only-via-CXO end state.

Why not Approach B (lean tpviz-local subscriber): it would duplicate the
non-trivial intermittent-subscribe / grace / resubscribe / snapshot logic that
already exists and is battle-tested in the visor, inviting drift. One shared impl
is better.

## Open items / risks

- Confirm `treestore.NewSubscriber` param type matches the reward server's dmsg
  client (visor uses `dmsgcurl` import; reward server uses `dmsgclient` /
  `StartDmsgEmbedded`) — likely the same underlying `*dmsg.Client`.
- The move must preserve the visor's existing netviz behavior + tests.
- Deployment: prod TPD must actually **publish** the uptime feed
  (`StartUptimeCXOPublisher` running) and populate it — verify after redeploy.
- `#3388` (HTTP-over-dmsg uptime fetch) stays as the fallback until CXO is
  validated live, then the HTTP polling path can be removed.
