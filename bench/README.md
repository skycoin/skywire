# bench — live route-multiplexing measurements

One directory per `<date>/<develop commit>`; every TSV row is one hash-verified
transfer through one proxy session, produced by `bench.sh`:

    bench/bench.sh <socks host:port> <sink base url> <bytes> <down|up> [label]

`down` GETs `<sink>/?bytes=N` from a `skywire cli proxy loadtest serve` sink and
checks the body's sha256 against the sink's `X-Sha256` header; `up` POSTs N random
bytes to `<sink>/upload` and checks the sink's sha256 of what it received. Columns:
`label dir bytes speed_Bps http got time_s hash_ok`. The first line of each file
names the exit, both ends' commits, the proxy session, its route and transport, and
the sink, so a row can be reproduced.

Rules (docs/design/route-multiplexing-test-plan.md): frozen exit (its update timer
stopped for the set), both ends on the same develop commit, one transfer at a time
unless the row says otherwise, references before subjects, no mux change merges
without a before/after row here.

## Reference sets, unattended

    bench/run-refs.sh <exit pk> bench/<date>/<commit> <pins dir> [trials] [sink]

produces `ref-direct-stcpr`, `ref-direct-squicr` (a `--direct` proxy each, with
`route settings --prefer` selecting the carrier) and one `ref-via-<short>` per pin
file `<pins>/via-<short>.json` (a proxy started with `--route`, so the session
runs on exactly that two-hop leg). Each set also writes `<set>.carrier.tsv`: per
row, the byte deltas of the transport the row is supposed to ride (and, for a
pinned route, the exit-side deltas of hop 2 over the whole set), so the carrier
claim in the header is checked against the transport counters rather than assumed.

## Mux sets, unattended

`bench/run-mux.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]`
takes the stream-level and packet-level sets after the references:

- `mux-tunnels-N` — `proxy start --tunnels N`: N independent tunnels, one
  route group each; the visor steers every extra tunnel onto a different
  first-hop transport.
- `mux-legs-N` — one routed proxy, then `proxy mux set --legs --prune` to
  exactly the first N pinned two-hop routes as legs of ONE route group. The
  group's `src_port` is checked before and after the set (it must not change).

`<set>.legs.json` is the `mux info --json` snapshot the set ran on (groups,
legs, transport types, remote pks); `<set>.carrier.tsv` has the per-row byte
deltas of every first-hop transport. `TUNNELS="2 3"` and `LEGS="2 3 5"`
choose the counts; `[pin order]` lists pin shorts best route first.

## Standby tunnel pool, unattended

`bench/run-standby.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]`
measures the standby tunnel pool: the session holds every disjoint route to the
exit as a dialed tunnel, and only `--tunnels` of them carry traffic.

One set per run, `mux-standby-<pool>`, where `<pool>` is the pool size the
session SETTLED at — the script starts the default proxy instance exactly as
`run-mux.sh` starts a `mux-tunnels-N` set (no pins, no extra flag: the pool is
on by default), then polls `visor state --select mux_route_groups` until the
group count to the exit has not moved for `POOL_QUIET` (20 s). The count it
finds names the set, so a run whose pool never grew past the active set is
recorded as `mux-standby-2`, marked INVALID, and cannot be mistaken for a full
pool. `<set>.legs.json` is the settled pool in `run-mux.sh`'s shape — groups,
legs, hops with full public keys, `remote_ip` per leg — plus each group's
`tunnel_role` when the visor reports one (`STANDBY_POOL=0` is the control run).

Rows are `run-mux.sh`'s twenty in the same order (1–5 10 MB down, 6–10 10 MB up,
11–15 50 MB down, 16–20 50 MB up). Row 11 — the first 50 MB download — is the
chaos row: `CHAOS_AFTER_S` (5) seconds in, the first-hop transport of one ACTIVE
tunnel is removed with `tp rm`, and the row is timed to the first byte that
arrives afterwards. The active tunnel is the one `tunnel_role` names, or, when
the visor does not report that field, the one whose first hop actually carried
the downloads of rows 1–10. What may be cut is fenced as in `run-degrade.sh` —
one of the pinned hop-1 stcpr transports (so `tp add -t stcpr <pk>` rebuilds the
same id), never the transport to the exit, never one a second group shares — and
a pool with no such candidate is INVALID before a row is measured. The transport
is re-added right after the row and every pin is swept again at the end; a pin
the sweep cannot restore is named, and `rig-restore.sh` is then the next step.

`<set>.chaos.tsv` records what was cut (row, transport id, first-hop public key
in full, timestamp, and the row's before/after goodput). `<set>.assert.tsv` is
the set's pass/fail table: rows 11–15 all hash-verified; every pre-cut route
group except the cut one still held afterwards (the no-rebuild test, with any
port the pool refills recorded separately); at least one `tunnel_promoted` — or
`leg_promoted`, and the row says which was found; time to first byte after the
cut under `CHAOS_TTFB_MAX_S` (2); and zero reorder wedges at either end, read
from the local event ring and both ends' recovery counters.
