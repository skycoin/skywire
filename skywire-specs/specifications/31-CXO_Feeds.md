# CXO Feeds

| Status   | Implemented                                                              |
|----------|--------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                            |
| Code     | `pkg/cxo/treestore`, `pkg/cxo/cxosub`, `pkg/cxo/cxoaggregate`, `pkg/cxo/cxoutils`, `pkg/cxo/sysfeed`, `pkg/skyenv` |

## Abstract

A CXO feed is a signed, content addressed tree of named leaves that a publisher
serves on a dmsg port. A subscriber receives each new root and fetches only the
objects it does not hold. Deployment services and visors use feeds to distribute
state, so that a reader can take a snapshot, and follow changes, without polling
an HTTP API.

## Overview

The feed tree (`pkg/cxo/treestore`) is built on the CXO object store
(`pkg/cxo/skyobject`). The publisher keeps the tree in memory, and after a
batch window it encodes the changed branches and saves a new signed root. An
unchanged branch keeps its hash, so a subscriber does not fetch it again. A
subscriber walks the filled tree into a path to bytes map.

Two connection directions occur.

- **Subscribe.** The subscriber dials the publisher at `<publisher-pk>:<port>`
  and subscribes to the feed key. Readers of the deployment feeds
  (`cxosub.Manager`) do this.
- **Announce.** The publisher dials an aggregator (`Publisher.AnnounceTo`). The
  aggregator holds the inbound connection and subscribes to the feed whose key is
  the connection's remote key. Visors use this toward TPD, dmsg discovery, the
  address resolver and service discovery.

## Addressing

The feed key is the publisher's public key (`Publisher.Feed`). A publisher MAY
serve several feeds under one key on different dmsg ports
(`treestore.PubConfig.DmsgPort`). The default port is
`cxotransport.DefaultCXOPort`, which is `skyenv.DmsgCXOPort` (50). A subscriber
creates a `treestore.Subscriber` with `SubConfig.DmsgPort` set to the same port
and calls `Connect` with the publisher key. The dmsg ports are in the table below
and in [01](01-Overview.md). A visor lists its feeds at `GET /feeds` on its dmsg
HTTP service as an array of `{"name", "dmsg_port", "description", "system"}`.

## Data Formats

**Paths.** A path is slash separated segments. A segment is not empty and does
not contain `/`. The empty path is the root. A prefix matches whole segments.

**Leaves.** A leaf is a byte string stored in the tree entry
(`treestore.TreeEntry.Leaf`). A single object is limited to
`skyobject.Config.MaxObjectSize` (16 MiB); a publish that contains a larger
object is refused. Most leaves are JSON, gzip compressed. Two encodings occur.

- A bare gzip body. A reader decompresses with `cxoutils.Gunzip`, which detects
  the gzip header and passes other bodies through.
- A frame: a version byte followed by gzip (`cxoutils.FrameGzip`,
  `UnframeGzip`). A reader skips a leaf whose version it does not know.

**Layout conventions.**

- `entry` and `tombstone`. Older feeds publish `<id>/entry` for a record and
  `<id>/tombstone` for its removal. Current feeds do not use tombstones. A record
  is removed by omitting it from its batch leaf or by deleting its leaf.
- A snapshot leaf replaces per record leaves where a reader needs the complete
  set. The reader treats absence as removal.
- A shard is a leaf for a subset of keys, with a fixed number of shards, so that
  a reader can fetch one subset.
- A day leaf, `<prefix>/day/<YYYY-MM-DD>`, holds one settled calendar day that no
  longer changes.

## Interfaces

### CXO Feeds

Every feed below is served by `treestore.Publisher` on the given dmsg port.
"Open" means no subscriber restriction (`SetAllowlist(nil)`).

| Port | Identifier | Publisher | Root layout | Cadence | Consumer |
|---|---|---|---|---|---|
| 50 | `DmsgCXOPort` | visor | `transports/telemetry/<sh>` and `transports/checkpoints/<sh>` with `<sh>` two hex digits from `00` to `0f`; `transports/<uuid>/<date>/timeline`; `tiers/<tier>/<date>`; `services/<svc>/<date>`; `tp-list` as a fallback | batch window 10 s; samples every minute | TPD aggregator |
| 69 | `DmsgVisorTPListCXOPort` | visor | `tp-list`: gzip JSON `{"version", "c": [CompactEntry]}` | batch window 2 s; republished every 90 s and on a transport change | TPD aggregator |
| 67 | `DmsgDMSGDRegistrationCXOPort` | visor | `entry`: JSON `disc.Entry` | batch window 5 s; on each registration | dmsg discovery |
| 71 | `DmsgVisorARBindCXOPort` | visor | one leaf per transport type, named by the type: JSON of the bound `LocalAddresses` | batch window 5 s; on each bind | address resolver |
| 72 | `DmsgVisorSDRegCXOPort` | visor | `services`: framed gzip (version 1) JSON array of `servicedisc.Service` | batch window 5 s; on each change | service discovery |
| 51 | `DmsgTPDMetricsCXOPort` | TPD | `metrics/day/<YYYY-MM-DD>`: gzip JSON, with `metrics/day/<date>/part/<NNNN>` if a day is too large | every 5 minutes for the current day | reward system, hypervisor |
| 52 | `DmsgTPDUptimeCXOPort` | TPD | `uptimes/days/<n>` for `n` in 1, 7, 30 | every 60 s | hypervisor |
| 73 | `DmsgTPDStatsCXOPort` | TPD | `stats/network`, `stats/versions`, `stats/daily` | every 12 s; `stats/daily` every 5 minutes | network view |
| 75 | `DmsgTPDRoutingCXOPort` | TPD | `routing/<pk>` snapshot shards and `routing/delta/<seq>` (12 digits) | every 10 minutes; a snapshot every 6 publishes | visors |
| 77 | `DmsgTPDVisorBWCXOPort` | TPD | `visorbw/day/<YYYY-MM-DD>`, the last 7 settled days | checks every 10 minutes | reward system |
| 79 | `DmsgTPDPerKeyCXOPort` | TPD | `perkey/stats` | every minute | network view, CLI |
| 53 | `DmsgSDServicesCXOPort` | service discovery | `services/<type>/all`: framed gzip JSON array of `servicedisc.Service`, sorted by key | batch window 1 s, on registration change | hypervisor network visualizer |
| 54 | `DmsgDMSGDClientsByServerCXOPort` | dmsg discovery | `clients-by-server/<server-pk>` presence leaf (version 2, no entries); `clients-by-server/servers/<pk>` signed server entry (framed gzip, version 1) | batch window 30 s | visors, dmsg servers, TPD |
| 74 | `DmsgARBindingsCXOPort` | address resolver | `<h2>/<h3>` for the third and fourth hex digits of the key: framed gzip JSON map from key to bindings, 256 buckets always present | batch window 30 s; full resync every 5 minutes | visors |
| 76 | `DmsgARReachCXOPort` | address resolver | `reach/<h>` for the first hex digit after the `02` or `03` prefix, 16 leaves | batch window 30 s | visors (autoconnect) |
| 78 | `DmsgConfCXOPort` | configuration bootstrapper | `services/current`: gzip JSON `deployment.Services` (`deployment.ServicesCXOPath`) | checks every minute; publishes on change | visors |
| 55 | `DmsgTPDAllTransportsCXOPort` | none | `transports/all/`. No publisher runs. | | |

A user feed has a name and a port chosen by the visor owner. The visor refuses a
port that belongs to a system service or to the chat pair range, and the name
`stats`.

### Access

A visor feed (ports 50, 69, 67) restricts subscribers to the visor's peer
whitelist plus the consuming service (`composeFeedAllowlist`). The restriction is
open only when both are empty. The deployment feeds in the table are open.

## Behaviour

**Publish.** `Put`, `PutBatch` and `Delete` change the in memory tree. After the
batch window the publisher encodes the changed branches and saves a root. When
nothing changes for `heartbeatInterval` (45 seconds) it republishes the current
root so that the connection stays alive. The publisher keeps the newest roots
(`PubConfig.KeepRoots`) so that a subscriber that is still filling an older root
can finish.

**Subscribe.** A subscriber fills the whole root before it can read a leaf. A
feed with few top level children fills faster. A subscriber that sees no root for
60 seconds reconnects (`reconnectQuietThreshold`). `cxosub.Manager` runs one
cycle per feed while a consumer holds it: subscribe, wait for the first root,
copy the tree into a snapshot, unsubscribe, and sleep for the interval.

**Aggregation.** An aggregator (`cxoaggregate.Core`) runs a CXO node on the
feed's port. It subscribes to each connection whose remote key is a feed key,
decodes the leaves of each filled root and passes them to the service. Leaves
that name a record a reporter does not own are rejected by the service. When
dmsg discovery, the address resolver or service discovery run in the TPD process,
visors may carry their registrations on the telemetry feed as leaves named
`reg.dmsgd.`, `reg.ar.` and `reg.sd.` followed by the leaf name used on the
dedicated feed (`pkg/cxo/sysfeed`).

## Security Considerations

The root signature proves which key published a feed. A leaf is not signed on its
own, so a service MUST check that the reporter is entitled to the record a leaf
names. TPD requires the reporter to be an edge of every transport it lists, and
dmsg discovery requires the feed key to equal the entry's `static`. An open feed
exposes its contents to any dmsg peer. A restricted feed exposes them to the
listed keys.

## References

- [04 Transport Discovery](04-Transport_Discovery.md)
- [05 Dmsg](05-Messaging_System.md)
