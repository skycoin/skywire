# Public Autoconnect

| Status   | Implemented                                                                                   |
|----------|-----------------------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire f37a464a4 |
| Code     | `pkg/visor/autoconnect*.go`, `pkg/visor/visorconfig/autoconnect.go`, `pkg/visor/visorcore`    |

## Abstract

Public autoconnect makes a visor open transports to other visors on its own. It
learns the public visors from Service Discovery, learns which transport types
can reach each peer from the Address Resolver reach feed, and dials in phases,
one transport type at a time, within configured limits. The transports it
creates carry the label `transport.LabelAutomatic`.

## Overview

Autoconnect uses three data sources. Service Discovery supplies the public visor
list (see Public Visors). The Address Resolver reach feed supplies a verdict for
each peer and type (see Address Resolver). The Address Resolver bindings feed
supplies the data for the site limit. Autoconnect does not use the Route Finder.

## Addressing

Autoconnect has no network interface of its own. It reads the CXO feeds
`FeedSDServices` (dmsg port 53), `FeedARReach` (port 76) and `FeedARBindings`
(port 74), and falls back to Service Discovery over dmsg HTTP.

## Data Formats

### Configuration

`visorconfig.Transport.PublicAutoconnect`, JSON key `public_autoconnect`, enables
the feature. `visorconfig.Autoconnect`, JSON key `autoconnect` in the same
section, limits it.

| JSON key       | Type            | Default | Meaning                                         |
|----------------|-----------------|---------|-------------------------------------------------|
| `site_links`   | int             | 2 (`DefaultAutoconnectSiteLinks`) | transports of the site types kept between two sites; 0 turns the limit off |
| `site_types`   | []string        | `sudph` | transport types the site limit applies to       |
| `max_per_type` | map string to int | built in per type | cap on automatic transports this visor dialed of a type; 0 means no cap |
| `max_total`    | int             | 0       | cap on all automatic transports this visor dialed; 0 means no cap |

A limit counts only the automatic transports the visor initiated. Reaching a
limit stops new dials and closes nothing.

### Built in caps

| Type     | Cap when `max_per_type` has no entry                       |
|----------|------------------------------------------------------------|
| `stcpr`  | none                                                       |
| `sudph`  | none                                                       |
| `squicr` | none, but 5 new dials per cycle (`maxPublicVisors`)        |
| `swtr`   | 5, or 20 on a visor without `stcpr`                        |
| `swsr`   | 5                                                          |
| `webrtc` | 20 (`maxWEBRTC`)                                           |

## Behaviour

### Timing

| Constant                  | Value  | Meaning                                       |
|---------------------------|--------|-----------------------------------------------|
| `initialAutoconnectDelay` | 3 s    | delay of the first pass after start           |
| `PublicServiceDelay`      | 300 s  | delay between passes (`skyenv.PublicAutoconnectInterval`) |
| `browserAutoconnectRetry` | 30 s   | next delay for a visor without `stcpr` that holds fewer `swtr` transports than its cap |

### Pass

A pass runs these steps.

1. When the visor is under resource pressure and holds at least two automatic
   transports, the pass ends.
2. Discover the public visors. Autoconnect reads `services/visor/` from the
   Service Discovery CXO snapshot first. If the snapshot has no visors and HTTP
   Service Discovery is configured, it asks Service Discovery over dmsg HTTP, with
   3 tries and a 20 s timeout each. If a fetch fails, the pass ends.
3. If no public visor is found, it asks Transport Discovery for per key statistics
   (`GetAllTransportsPerKeyStats`) and uses every visor other than itself with at
   least 5 transports.
4. A phase is skipped when the transport manager does not know the type or the
   transport creation policy denies it (`CanCreateTransport`). If every phase is
   skipped, the pass ends. The `swsr` phase is also skipped on a browser page
   served over HTTPS.
5. Run the phases below. Within a phase, a peer is skipped when the visor already
   holds a transport of that type to it, or when the visor is the peer.

### Phases

| Phase | Type     | Targets                                                              |
|-------|----------|----------------------------------------------------------------------|
| 1     | `stcpr`  | public visors without an `stcpr` transport                           |
| 2     | `squicr` | public visors without a `squicr` transport                           |
| 2b    | `sudph`  | public visors without any automatic transport, after site filtering  |
| 3     | `sudph`  | peers the reach feed lists as bound for `sudph`, which are not public; skipped on a public visor |
| 3b    | `swtr`   | public visors phase 1 handled that have no direct automatic transport; peers reached over `webrtc` or `dmsg` first |
| 3c    | `swsr`   | the same targets that `swtr` did not reach                           |
| 4     | `webrtc` | public visors phase 1 handled that have no direct transport or `webrtc`, and non-public peers that declare they accept `webrtc`, after site filtering |

A visor that cannot create `stcpr` takes every public visor it has no direct
transport to as a target of phases 3b and 3c, in random order.

### Reach verdicts

For each type a peer has one verdict from `arfeed.Reach.Verdict`: `Confirmed`,
`Capable`, `Untested` or `Unreachable`. A phase dials `Confirmed` targets first,
then `Capable`, then at most 3 `Untested` ones (`reachExplorePerCycle`). It does
not dial `Unreachable` targets. The verdict is `Untested` for every peer when the
reach feed has not synced. `swsr` uses the verdict of `stcpr`, because it rides
the `stcpr` port.

The phase target count is limited by the per type cap, by `max_total` and, for
`squicr`, by the per cycle budget.

### Site limit

A site is a set of visors that share a public IPv4 address and are linked by a
common private `/24` network among their reported addresses. The site map is built
from the bindings feed. For two sites, `site_links` pairs of visors, taken as the
pairs with the lowest hash of the two keys among members that serve the type
(`pairScore`), may hold a transport. A visor that is not among those pairs is
skipped for the type. The limit applies to the types in `site_types`. It does not
apply when the feed has not synced, or when either visor is not in a site.

### Resource pressure

The visor is under pressure when its available memory falls below the smaller of
10 percent of RAM and 256 MiB, or when the garbage collector takes more than 25
percent of the CPU over one minute. It leaves pressure when memory is above the
smaller of 20 percent and 512 MiB and the collector share is below 10 percent
(`pkg/visor/pressure.go`). The state is evaluated every 15 s.

## Security Considerations

Autoconnect connects to any visor that Service Discovery or the reach feed lists.
Transport establishment authenticates the peer with the transport handshake. The
caps bound the number of automatic transports a visor dials.

## References

- Public Visors
- Address Resolver
- Service Discovery
- Transport Management
