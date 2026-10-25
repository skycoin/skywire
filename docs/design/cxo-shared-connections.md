# Shared CXO connections for co-hosted services

Status: proposal. It builds on [One CXO feed per visor](cxo-one-feed-per-visor.md)
(#5718), which moved every visor's registrations onto its system feed.

## Problem

A visor holds a separate CXO connection for every service feed it publishes to
or reads from. Each feed is its own CXO node on its own dmsg port, so each needs
its own dmsg stream, noise handshake, buffers and receive goroutine. #5814 cut
the goroutines of an idle connection from four to one. The connections remain.

prod01 runs every deployment service in one visor process, each under its own
key. Its CXO connections on 2026-10-25, from `cli visor state --via
skynet://<prod01> --select services`:

| Service | Feed | Port | Conns |
|---|---|---|---|
| address resolver | `ar-bind` aggregator (legacy) | 71 | 705 |
| address resolver | `bindings` publisher | 74 | 704 |
| address resolver | `reach` publisher | 76 | 18 |
| transport discovery | `telemetry` aggregator | 50 | 842 |
| transport discovery | `routing` publisher | 75 | 178 |
| transport discovery | other publishers | 51, 52, 73, 77 | 5 |
| service discovery | `sd-reg` aggregator (legacy) | 72 | 613 |
| service discovery | `services` publisher | 53 | 21 |

That is 3,086 connections. The legacy aggregators on 71 and 72 serve visors
older than v1.3.102 and go away on the schedule in the #5718 note. About 370 of
the 1,000 online visors were older than v1.3.102 that day.

The rest is the subject here. Once #5718 has landed everywhere, a visor still
holds the system feed connections to TPD (the announce conn and TPD's dial-back)
and one more connection for each service feed it reads. About 700 visors read
the address resolver's `bindings` feed. Some also read TPD's `routing` feed, SD's
`services` feed or AR's `reach` feed. Those feeds come from one process on one
host, but each arrives on its own connection because each service runs its own
node under its own key.

## Proposal

### One host node for co-hosted feeds

A host that runs several services gives their publishers one CXO node on one
dmsg port under the host visor's key. A CXO connection already carries several
feeds (`node.Conn.Subscribe`), and a node already shares several
(`node.Node.Share`). Each service keeps signing its own feed with its own key,
so a subscriber verifies every Root exactly as it does today. Only the
connection that delivers them is shared.

A service mounted under the visor's own key already gets the visor's node for
its aggregator port (`visorCXOHost` in `pkg/visor/embedded_services_cxo.go`).
This extends that to the publishers of services under their own keys. Each
publisher keeps its own allowlist, so the host node has to check it per feed
when a peer subscribes.

### Finding the host node

#5718 already tells a visor which services share TPD's process. The host
publishes a `local` transport to each co-hosted service and labels it
`sysfeed`. The host adds a second label, `cxohost`, to the links of services
whose feeds its host node serves. A visor that sees the label subscribes to all
of those feeds on one connection to the host node. Without the label it uses
the per-service feeds as today.

A wrong label cannot forge data, because every Root is still signed by the
service's key. It can only withhold feeds, and the visor falls back to the
per-service feed when the host node does not serve one it subscribed to.

### Keys stay as they are

Moving the services onto the host visor's key was considered and is not part
of this proposal. Visors find the services by their keys (`deployment.Prod` and
the conf service), so a key change needs every visor to learn new addresses.
What a shared key would save is already gone: the services' dmsg clients reach
the host's own dmsg server through in-process pipe sessions (#5718), and with
this proposal the CXO connections are shared without a shared key.

So services keep their own keys, and a deployment can still run each service
in a separate process under its own key. A service that is not co-hosted never
gets the `cxohost` label, and visors keep a connection per feed for it.

## Transition

The same steps as #5718. Old visors keep their per-service connections. New
visors keep them until TPD shows the `cxohost` label.

1. Merge. The host node starts and serves the co-hosted feeds next to the
   per-service nodes, which keep running.
2. prod01 labels the links. Develop visors switch on their next restart, apt
   boards when they take the release that carries this.
3. Watch the `bindings`, `routing`, `services` and `reach` connection counts
   fall and the host node's rise by less, and check that AR lookups and route
   finding stay level.
4. Remove the per-service publisher nodes for co-hosted services two releases
   later, or 30 days later, whichever is later, if they then hold under 2% of
   the fleet.

## Expected savings

At about 1,000 visors the `bindings` feed alone is about 700 connections and
the other read feeds about 220. Sharing a connection for all of a visor's read
feeds would save roughly 900 connections on prod01, plus one dmsg stream and
one noise handshake each on every visor. These are estimates from the counts
above, not measurements.

## Open questions

- One connection now carries a large feed (`routing`) next to small ones
  (`bindings`). CXO sends whole messages, so a large Root fill can delay a
  small update on the same connection. This needs a measurement before step 2.
- Whether the system feed's two connections per visor (announce and TPD's
  dial-back) can become one. The #5718 note keeps both, and this proposal does
  not change them.
