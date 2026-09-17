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
deltas of every first-hop transport. `TUNNELS` and `LEGS` choose the counts
(both default to `2` — see the knob table below); `[pin order]` lists pin
shorts best route first.

## Paired references (the 2026-09-17 goal rules)

A reference set measured an hour before the subject is not a bar: the 50 MB
download reference was 8.84, then 6.63, then 5.43 MB/s in three consecutive
windows on 2026-09-16. So the campaign measures its reference **next to every
row**:

    PAIRED=1 (default)  one reference row of the SAME cell runs immediately
                        BEFORE each mux row, on ONE single route, in a second
                        proxy instance (`skysocks-client-ref` on :1081 — never
                        through :1080, which stays the instance under test).
                        The two never transfer at the same time, so a 20-row
                        set is 40 transfers.
    PAIRED_REF=<pin|direct|auto>
                        the reference route. `auto` reads `paired-ref.txt`
                        (below), then the newest `ref-*` medians in the out
                        dir, then `drift.tsv`, then falls back to `direct`.
    PAIRED=0            restores exactly the un-paired behaviour.

Each set writes `<set>.paired.tsv` — row, cell, ref MB/s, mux MB/s, ratio,
ref_ok, mux_ok, ref_legs — and `<set>.paired-rows.tsv`, the reference transfers
as plain `bench.sh` rows.

`bench/verdict.sh <refs dir> <mux dir>` scores the **median paired ratio** per
cell when that file exists, and says `paired` in its mode column; with no paired
file it falls back to the old best-reference bar and says `bar`. PASS needs

- a download cell at ratio **>= 0.95** (1.0 within the goal's 5 % noise band),
- a single upload cell at ratio **>= 0.95**,
- a composition set (`mux-compose-*`) **not worse than either** `mux-tunnels-2`
  or `mux-legs-2` at 10 MB, and **>= the better of the two** at 50 MB and
  100 MB — all three paired to the same route in the same run,
- every row hash-verified, and wire/goodput <= 1.2.

### Which route the references ride

    bench/pick-ref.sh <out dir> <pins dir> [exit pk] [sink]

takes the top three routes of the last full `run-refs.sh` suite, probes each
with two 50 MB downloads **now**, and writes the winner to
`<out dir>/paired-ref.txt`, which `run-mux.sh` and `run-compose.sh` read when
`PAIRED_REF` is unset. Six transfers, about a minute.

That makes the eight-set reference suite a **once-a-day route-ordering run**
rather than the campaign's bar, and it makes `bench/drift-probe.sh`
unnecessary whenever `PAIRED_REF` is in play: drift asks whether a stale bar
has moved, and a paired campaign has no stale bar. The probe is a download only
— upload medians repeat within 5 % across runs (direct 50 MB up 9.52 / 9.60 /
9.65 / 10.32), downloads swing by 2x.

## The rest of the campaign knobs

| knob | default | what it does |
|---|---|---|
| `TUNNELS` / `LEGS` | `2` / `2` | the default suite. Three tunnels and three legs never win: `TUNNELS="2 3" LEGS="2 3 5"` runs them on demand. |
| `COMPOSE` | `2x2` | `2x1` — two tunnels of one pinned leg each — is the hand-picked ceiling check (best single cell of campaign20, 8.33 MB/s): `COMPOSE="2x2 2x1"`. |
| `[trials]` / `TRIALS_UP` | `5` / `3` | download cells take five trials, upload cells three. Uploads repeat themselves; downloads do not. |
| `SIZES` / `CELL100` | `10 50` | cell sizes, in megabytes or bytes. `CELL100=1` adds the 100 MB **download** cell criterion 4 asks for (`DIRS100="down up"` measures its upload too). |
| `CUT_CELL` / `CUT_TRIAL` / `CUT_ROW` | `50down` / `3` / derived | every campaign includes a cut row: `CUT_AFTER_S` (5) seconds into that row one route group loses its first-hop transport and the set carries on. With 5/3 trials that is row 11; `CUT_ROW=<n>` names a row outright and `CUT_ROW=0` turns it off. |
| `UP2` | `0` | adds `mux-tunnels-2-up2`: two CONCURRENT 50 MB uploads whose summed MB/s is scored against the sum of the two best single-route references. |
| `EXIT_SNAP` | `1` | the exit's view of the set's route groups at set start, after the cut row and at set end (`start` / `cut` / `end` in the row column) — not once per row, which cost up to 40 s of dead time per row. |
| `EXIT_RES` | `1` | `bench/exit-resources.sh` around every set and `bench/exit-resources-check.sh` after it. |

## Degradation, and the exit resource gate

`bench/run-degrade.sh` (criterion 6) is a whole set of cut rows; `run-mux.sh`'s
`CUT_ROW` is one. Both take the cut from `bench/lib-cut.sh`, with the same
fences: never the transport to the exit, never a first hop a second route group
shares, and never one that cannot be restored to the same transport id.
`<set>.cut.tsv` records the row, the transport, the first hop's full public key,
the timestamp, `ttfb_after_cut_s` and the surviving route group ports before and
after.

The cut also never targets the **paired reference's own route**. campaign21 chose
Atlanta's first hop 95839ad0-b588-0b1d-8475-45fba6ae993c under `CUT_FENCE=auto`
while `paired-ref.txt` named that same route (`0371ab4b`) for the :1081
reference instance, so the reference lost its route at row 11 and the paired
ratios of rows 12–16 measure the mux session against a rebuilt reference rather
than the one rows 1–10 saw. Fenced off now: the first-hop transport of the
reference route (resolved from the same pin file `lib-paired.sh` starts the
instance on) and every transport the `skysocks-client-ref` / `-ref2` route
groups hold. When that leaves no candidate the set carries **no cut row** —
`<set>.cut.tsv` holds `# cut=skipped:<reason>` and `verdict.sh` prints it —
because cutting the reference costs every later paired row of the set;
`CUT_REF_FENCE=0` restores the old choice.

    bench/exit-resources.sh <out dir> <label>          # one reading
    bench/exit-resources-check.sh <out dir> <set>      # score the pre/post pair

The reading is taken over `pty exec` on the exit: RssAnon and CPU seconds of the
skywire unit's MainPID, the 1-minute load, and the idle CPU rate sampled inside
the reading. The gate FAILS a set whose RssAnon grew by more than **64 MiB** or
whose CPU per wall second ran more than **half a core** above the pre-set idle
rate. A failed check keeps every result and makes the run script exit non-zero
at the end; a reading that could not be taken is SKIP, not FAIL.

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
## The standby pool changes what "shape" means

`proxy start --standby-pool` (#4986) holds more route groups than `--tunnels N`:
the active set plus standby tunnels that are dialed, kept alive and measured on
the same 5 s ping but carry no streams, so a tunnel that dies is replaced by a
route that already exists instead of one set up from scratch. A live `--tunnels
2` therefore comes up with **three** groups — two active, one standby — and the
pool keeps filling one dial at a time after the active set is up.

So every shape check counts the **active** groups, from the `tunnel_role` field
`proxy mux info --json` puts on each group. The field is `omitempty`: when no
group carries it (any binary before the pool) every group counts as active and
the checks behave exactly as they did. The set header records
`route_groups=<all> active=<n> standby=<m>`, `<set>.legs.json` keeps the role
per group, and `run-mux.sh` / `run-compose.sh` wait for the group count to hold
still for 10 s (bounded at 60 s, `POOL_STABLE_S` / `POOL_WAIT_S`) before
snapshotting, then print the pool size they settled at. The carriers are still
named from every group, standby included, because a standby leg can be promoted
mid-set; the cut row only ever targets an active one.
