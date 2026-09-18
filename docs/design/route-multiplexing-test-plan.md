| striped upload | `upload.stripe_min_bytes` · `upload.chunk_bytes` · `upload.mem_bytes` · `upload.concurrency` · `upload.replay_max_bytes` · `upload.probe_ttl` |
| upload retries | `upload.ack_timeout` · `upload.idle_timeout` · `upload.durable_wait` · `upload.resend_passes` · `upload.early_tries` · `upload.early_wait_max` · `upload.busy_backoff` · `upload.busy_tries` · `upload.replay_tries` |
# Route multiplexing: the live campaign to finish it

Status: plan, 2026-09-15. To be taken up after v1.3.94. "Tested" here means
tested live, on the fleet, until the implementation is actually right; the
unit and CI material is an appendix, because it is the safety net, not the
work.

## 0. Where it stands

What works, proven live:

- packet-level striping in both directions (exit and node both spray across
  legs), transport-disjoint legs, same-LAN artifact legs rejected (#4253);
- route setup stands up N disjoint legs without a rebuild (#4116, #4131);
- the retransmit storm is gone (#4399: per-seq backoff, RTT-floored RACK);
- the reorder buffer never skips (#4005), so the noise stream is never
  corrupted;
- a single black-holing leg is probed and replaced (#4247); latency-band
  admission parks outliers (#4248); ECF is the default scheduler with a
  baseline-RTT BDP, congestion shed and a cold budget (#4254);
- per-direction leg assignment with a load-driven flip exists (#4304, #4305)
  but its live demo never happened: the exit was still on the previous build;
- the rig exists: `proxy loadtest serve/run`, `proxy mux info --ndjson`,
  `proxy mux set/mode/cap/width/direction`, `visor state --via dmsg://<exit>`.

What does not work, and is the whole point:

- **more legs = less throughput, monotonically, both directions**, with
  healthy converged legs and a healthy single-leg baseline:

  | direction | 1 leg | 4 legs | 6 legs |
  |---|---|---|---|
  | download (exit-scheduled) | 350 KB/s | 144 | 12 |
  | upload (client-scheduled) | 7.1 MB/s | 5.8 | 3.5 |

  Send-side governance is ruled out (the upload row is scheduled entirely by
  the node that has every fix). The mechanism is the global-sequence no-skip
  frontier plus **unbounded per-leg in-flight**: nothing stops a slow leg's
  queue from growing, the frontier waits on it, and every added leg is another
  queue to wait on.
- the false liveness teardown is only partly fixed (#4346 halved it; the rest
  are conn-error reconnects driven by the same wedges);
- the adaptive default has never been shown to beat a single route on the
  same path, so "adaptive by default" is a belief, not a measurement.

The only proof that aggregation is possible at all is a static hand-set mux
of six homogeneous 175–312 ms legs that did about 2× a single leg, then was
not touched. Everything dynamic loses to that.

## 0b. The intended default, in the operator's words

Stated 2026-09-15, and it is the specification the rest of this document serves:

- Forward traffic goes over a direct route when one exists and is allowed;
  otherwise over the lowest-latency route available. Reverse traffic fans out
  over multiplexed multihop routes. Unidirectional whenever that is available.
- The direction switches on demand when upload bandwidth exceeds download
  bandwidth.
- It always decays to a working connection, even if that ends up
  bidirectional over a single transport or a single multihop route.
- Establish as many routes to the exit as can exist and hold them in standby,
  so they can switch in at a moment's notice. That may be a lot of routes in
  standby, and that is fine.
- The proxy already converts a GET into range requests when the server
  supports it, so the reverse path carries many streams in parallel.

Two consequences for the work below. First, the standby pool is large by
design; the campaign tunes how standby legs are chosen, ordered and promoted,
not how few there are. Second, the range-request case makes the reverse path
a set of independent streams, and independent streams do not need to share
one reorder frontier: pinning each stream to one leg (stream-to-leg affinity,
ordered per leg, no cross-leg reassembly) aggregates across streams with no
frontier at all, and is a smaller change than per-leg sequencing. A single
large stream (a non-range download, a VPN flow) still needs the frontier work
in §3.3, so both remain, with affinity first because it serves the case the
proxy already produces.

## 1. Exit criteria, measured live

The campaign is done when all of the following hold on the rig in §2, for
downloads and uploads, at 3, 10, 50 and 100 MB, five trials each:

1. **Completes.** 5/5, and the received bytes hash to the sent bytes.
2. **Aggregates.** N-leg goodput ≥ single-leg goodput on the same exit,
   measured concurrently, for N = 2, 4, 6; and 6 ≥ 4 ≥ 2 within tolerance.
   Anti-aggregation of any degree fails the criterion.
3. **Stays up.** The route group's local port is constant for the whole run
   (no rebuild); leg changes carry a named reason and stay under a bound per
   minute; no liveness teardown while any leg is live.
4. **Does not amplify.** Wire bytes / goodput ≤ 1.2 with no loss present;
   retransmits obey the backoff.
5. **The default is the winner.** Whatever policy ships as the default is the
   one that won the table. If a single route wins on a path, the default
   must fall back to a single route on that path, by measurement, not by
   configuration.


Since **v4 (2026-09-18)** two of those bars are measured against the ENDPOINT
rather than against another skywire number, so a campaign also runs
`bench/run-ceiling.sh <exit pk> <out dir> [trials] [sink] [pins dir]`: `CEIL_N`
(3) concurrent proxy clients, one 50 MB transfer alone and then all N at the
same instant, uploads first and then downloads, every transfer hash-verified
against the sink. It writes `ceiling.tsv` into the campaign directory — one row
per (kind, clients, trial) carrying the sum, the per-client `route:rate` pairs
and the hashes, plus a `# ceiling` line per kind saying whether the sum grew
from one client to N — and `bench/verdict.sh` takes the median of the concurrent
sums as the uplink and the downlink ceiling. With no `ceiling.tsv` both verdicts
keep the pre-v4 rule and print "no ceiling row": a ceiling quoted from an older
campaign is not a bar, because the bars move by 2x inside an hour.

The two kinds do **not** ride the same path, and that is the point. The uplink
is the local card, so its N clients are `--direct` (9.04 alone / 10.70
concurrent on 2026-09-16). The downlink over those same direct clients came out
at 4.72 alone / 5.04 concurrent while one download through the best intermediate
was doing 8–9.5 MB/s in the same hour: the direct stcpr path is **itself** the
download bottleneck, so a downlink ceiling measured through it measures that
path rather than the endpoint, and criterion 4 would clear it trivially. So the
downlink clients are pinned to the **N best distinct routes of the paired
ranking** — `paired-ref.tsv` when `bench/pick-ref.sh` has probed one, else
`paired_rank`'s medians, read from the out dir and then its parent; slot 1 the
best, slot 2 the second best, `direct` appended last when fewer than N ranked
routes exist. One extra `uplink-via` row per trial uploads over the best via
route alone, so the file also says whether the direct uplink is a bottleneck.

Criterion 10's spread policy is `SPREAD=1 bench/run-mux.sh …`, which adds the
`mux-spread-3` set — the default pool session, no pins and no `--tunnels`, with
`SETTINGS="spread.max_share=0.4 spread.min_routes=3"` merged into whatever the
campaign already sets, 50 MB down and up, paired like every other set and with
no cut row (a cut moves bytes between routes mid-set, which is the quantity the
cap measures). `bench/direction.sh` then turns both ends' per-leg counters into
per-row shares and `mux-spread-3.assert.tsv` scores them: `routes_active >= 3`,
`max_share <= 0.4 + chunk/size` (0.484 at 50 MB with a 4 MiB chunk — a scheduler
placing whole chunks cannot land on 40.000 %), the paired ratio of each cell
`>= 0.8`, hashes n/n, and the policy itself recorded, so a knob the binary
refused reads FAIL instead of passing as a measurement of the default.

## 2. The rig, frozen and repeatable

Everything the last campaign learned the hard way, fixed as procedure:

- **One controlled exit** on Linode (02f9aa58… or 02ea1b80…), auto-update
  timer stopped for the run, process uptime recorded at start and end so a
  collapse can never again be blamed on a restart. Both ends run the **same
  commit**; the deploy is the merge (fleet ripple) or, when the exit lags, the
  manual binary copy recipe. Never start a run with mismatched ends.
- **Two clients on the node, same exit:** A = one leg, the reference;
  B = the subject with N legs. Dialed **sequentially**, each session confirmed
  before the next (parallel dials contend in route setup and only one
  connects). Pulled **concurrently**, so the exit's condition is the same for
  both. B ≥ A is the test; A alone tells you the mesh's state that hour.
- **No reconfiguration during a run.** Every `proxy mux set`, `mode`,
  `width`, `cap` or policy change is its own run, from a fresh session.
  `proxy start` on a live app drops its route; measuring mid-re-establishment
  produced every false "storm" of the last campaign.
- **Measurement:** `proxy loadtest run` NDJSON (goodput, gaps), `proxy mux
  info --ndjson` every second, exit-side `visor state --via dmsg://<exit>`
  before and after (per-leg sent/recv/retx are the decisive diagnostic;
  logs are not). Results land in `bench/<date>/<commit>/` with the command
  lines that produced them. From a **public node** for NAT-free numbers when
  the question is the mesh, from this node when the question is the client.
- **A failed row is diagnosed where it fails.** Beside the `# exit-on-fail`
  line every runner already writes into `<set>.recovery.tsv`, an `http=000` row
  now also gets a `# blackout-capture <row>` line —
  `goroutines=<n> readch_full=<n> write_timeout=<n> rg_full=<desc|none>` — and
  the four files it summarises: `<set>.row<row>.goroutines-serve.txt` and
  `.goroutines-data.txt` (the shared inbound loop and what it is parked on),
  `.intake.json` (`visor state --select diag` `.intake`, whose
  `route_group_queues` names the group whose readCh is at capacity) and
  `.visorlog.tail`. These blackouts are a **local** receive-loop stall, not the
  exit's, and the evidence clears within a minute, so the capture runs first on
  the failing row, before the exit-side snapshot; it is bounded to ~60 s and
  `BLACKOUT=0` turns it off (bench/lib-blackout.sh).
- **The cut row cuts something that was carrying.** `run-standby.sh`'s chaos row
  picks the busiest ACTIVE tunnel that passes the restore fences — carrier
  growth orders the active candidates rather than choosing among all of them —
  and records `cut_target_role` in the cut log and the assert table. Cutting a
  standby tunnel takes out a few hundred bytes of keepalives, needs no promotion
  to recover and proves nothing about recovery, so when no active tunnel passes
  the fences the run says so and `promote_event` drops to INFO instead of
  failing a session that was never asked to promote.
- **Hygiene:** never remove transports the operator's own proxy uses; never
  let a same-LAN or NAT-hairpin leg into a subject group; watch the exit's
  own leg counters, because the exit runs its own scheduler.
- **The baseline table first.** Before any change: v1.3.94, the full grid of
  §1, both directions. Every later run is a diff against it.
- **References, unattended:** `bench/run-refs.sh <exit> bench/<date>/<commit>
  <pins>` takes the direct references (stcpr, squicr; a `--direct` proxy each,
  `route settings --prefer` choosing the carrier) and one proxy per pinned
  two-hop route (`proxy start --route <pin>`, the session on exactly that leg),
  ten transfers per size and direction, each hash-verified, with the carrier
  transport's byte deltas recorded per row and the exit-side hop-2 deltas per
  set. A pin lands only once the setup nodes run #4923 (a folded dmsg-server
  visor as an intermediate) and the client #4924 (`--route` dials a routed
  group instead of the AppDirect shortcut); `skywire cli proxy mux info -n
  <session> -v` must show the pinned first-hop transport as the only leg.

## 2b. Sweeping a value without a redeploy

Six of the eight rig cycles of 2026-09-16/17 changed a NUMBER, not a formula,
and each still cost a rebuild, a binary push and a restart — roughly three hours
of the two-day window spent deploying constants. `proxy settings` and the mux
half of `route settings` remove that cost: the value lives in an atomic the use
site re-reads, the visor holds the set per app, and a running skysocks-client
PULLS it on the keepalive tick it already runs (one `tunnel.probe_interval`,
5 s by default). Nothing is persisted — a knob lives for as long as the app
process does, so a restart is always a return to the compiled defaults, and a
run that sets nothing is byte-for-byte the old binary.

**Client knobs — `skywire cli proxy settings [--app skysocks-client]`**

| group | knobs |
|---|---|
| standby pool | `pool.fill_interval` · `pool.retry_backoff_base` · `pool.retry_backoff_max` · `pool.retry_rounds` · `pool.standby_rtt_stale` |
| tunnel promoter | `tunnel.promote_interval` · `tunnel.promote_margin` · `tunnel.promote_hold` · `tunnel.park_min_hold` · `tunnel.audition_window` · `tunnel.audition_every` |
| probing and metering | `tunnel.probe_interval` · `tunnel.liveness_interval` · `tunnel.rtt_alpha` · `tunnel.meter_sample_min` · `tunnel.meter_cap_decay` · `tunnel.meter_fresh` · `tunnel.exit_open_penalty` |
| range-split | `chunk.max_bytes` · `chunk.concurrency` · `chunk.retry_budget` · `chunk.idle_timeout` · `chunk.free_retries` · `chunk.outstanding_factor` |
| chunk plan | `chunk.probe_bytes` · `chunk.min_bytes` · `chunk.per_tunnel` |
| striped upload | `upload.stripe_min_bytes` · `upload.chunk_bytes` · `upload.mem_bytes` · `upload.concurrency` · `upload.replay_max_bytes` · `upload.probe_ttl` |
| upload retries | `upload.ack_timeout` · `upload.idle_timeout` · `upload.durable_wait` · `upload.resend_passes` · `upload.early_tries` · `upload.early_wait_max` · `upload.busy_backoff` · `upload.busy_tries` · `upload.replay_tries` |
| spread policy (v4) | `spread.max_share` · `spread.min_routes` · `spread.endgame` · `spread.weight` |

`chunk.max_bytes` and `chunk.concurrency` OVERRIDE the boot flags
(`--range-chunk-kib`, `--range-concurrency`): unset, the flag still wins.

The chunk-plan three are the granularity sweep of §3: `chunk.probe_bytes` is
chunk0 — the size probe, and the no-split threshold — while `chunk.min_bytes`
and `chunk.per_tunnel` are the floor and the per-tunnel chunk count the target
is computed from. They are read per download, so a sweep lands on the next
object, never mid-object.

The four upload knobs a test shrinks — `upload.stripe_min_bytes`,
`upload.chunk_bytes`, `upload.mem_bytes`, `upload.concurrency` — override the
compiled value the same way. `upload.chunk_bytes` is a CEILING, not a fixed step:
each object's chunk is planned from its Content-Length the way a download's is
(`planUploadChunk` — the object over two chunks per active tunnel, floored at
`chunk.min_bytes`, capped here), so 10 MB over two tunnels is cut at 2.5 MB while
50 MB stays at the 4 MiB ceiling. The planned size is snapshotted per object (the
sink addresses a chunk by its offset), and `slots()`/`headroom()` read chunk,
memory and concurrency from ONE snapshot per call, so a sweep landing mid-upload
cannot over-subscribe the sink's window and make it evict an acked chunk.

**Router knobs — `skywire cli route settings`**

`--ecf-max-window` · `--ecf-min-window` · `--ecf-window-margin` ·
`--send-window-wait-max` · `--leg-park-min-hold` · `--dead-route-hold` ·
`--dead-route-hold-max` · `--mux-fec`. Visor-wide and
per-end, like `proxy mux cap`; `--mux-fec` reaches route groups built after it,
since FEC is negotiated when a group is created. `windowRefreshInterval` is
NOT here: it becomes a per-route-group ticker when the group is built.

One sweep cell, start to finish:

```
skywire cli proxy settings                                  # the table, with pending/applied
skywire cli proxy settings chunk.max_bytes=8MiB chunk.concurrency=16
skywire cli proxy settings --json | jq '.knobs[] | select(.state=="applied")'
skywire cli proxy loadtest run -n sweep -u http://<host>/50M -d 2m -o bench/<date>/<commit>/chunk-8MiB.ndjson
skywire cli proxy settings --reset chunk.max_bytes chunk.concurrency
```

A row is valid only once its knobs read `applied` — a `pending` row means the
app has not pulled the change yet and is still measuring the previous value.

**In the bench runners — `SETTINGS`, and `bench/run-sweep.sh`**

A knob set by hand before a runner is gone by the first row: the visor's per-app
store is cleared when the app stops, and every runner in `bench/` STARTS the app
under test itself, once per set. `SETTINGS="key=value ..."` is therefore honoured
by `run-mux.sh`, `run-compose.sh`, `run-standby.sh` and `run-degrade.sh` at the
one point where it is both installable and covers the whole set — after the dial,
the shape check and the warm probes (and, where a pool is awaited, after it has
settled) and before the first row. `bench/lib-settings.sh` holds the single copy:
it applies the knobs, WAITS for them (polling `proxy settings --json` until
nothing reads `pending`, capped at three `tunnel.probe_interval` ticks or
`SETTINGS_WAIT`, 20 s), records them in the set's `#` header line and dumps the
full table to `<set>.settings.json`. A set whose knob never landed says
`settings_pending=` in its header rather than quietly measuring the compiled
default. `ROUTE_SETTINGS="--flag value ..."` does the same for the router knobs
and is RESTORED at set end from the `route settings --json` state read before it,
because a router knob outlives the app it was set for. The paired reference
instances receive neither: they are the control, and a sweep is only readable if
the reference is the same on every value. Every consumer of a pin file — the
paired reference, `run-mux.sh`'s legs pinning, `run-compose.sh`'s per-tunnel
slices, `run-degrade.sh`'s tunnels-2 targets — first runs `pin_ok`
(`bench/lib-pins.sh`): a pin whose first hop is not a transport UUID is refused
with a line starting `INVALID pin`, and a set that ends up without its reference
prints `unpaired`. On 2026-09-18 three pins had been overwritten with 59-byte
stubs and every set of a chain ran unpaired without one line naming the cause.

`bench/run-sweep.sh` drives one knob across a list of values — a complete run of
the named runner per value, into its own directory, with the knob as the only
difference:

```
TUNNELS=2 LEGS="" bench/run-sweep.sh <exit> bench/2026-09-18/<commit> $S/pins \
    run-mux.sh upload.chunk_bytes 1MiB,2MiB,4MiB,8MiB 3 http://127.0.0.1:18080 "<order>"
```

That writes `bench/2026-09-18/<commit>/sweep/upload.chunk_bytes=1MiB/` … `=8MiB/`
and tables them in `sweep/upload.chunk_bytes.tsv`: one row per value, one column
per (set, size, direction) cell, each holding
`median MB/s;paired ratio;hashes ok/n;wire/goodput`. Read the RATIO column, not
the median — the bar swings 2x inside an hour, and only the paired ratio says
whether a value moved the result or the network did.

**Spending the rig minutes where they pay — `bench/tune`**

A sweep pays the same price for every value of every knob, and three knobs at
three values is nine runs that still say nothing about how the three interact.
`bench/tune` spends the same budget as a bandit instead: one arm per (knob,
value), a Gaussian posterior per arm, and Thompson sampling — each round draws
one value from each arm's posterior, runs the runner ONCE at the argmax with
every other knob held at its incumbent (that knob's posterior-mean-best value so
far; a knob never yet measured is simply left out, so the app keeps its compiled
default), and updates that one arm. The knob under test rotates round-robin.

```
go run ./bench/tune -exit <exit> -out bench/2026-09-18/<commit> -pins $S/pins \
    -runner run-mux.sh -trials 2 -rounds 12 -env 'TUNNELS=2 LEGS=' \
    -args 'http://127.0.0.1:18080 "<order>"' \
    -objective 'mux-tunnels-2/50up:ratio,mux-tunnels-2/10up:ratio' \
    -knobs 'upload.chunk_bytes=1MiB,2MiB,4MiB;upload.concurrency=2,4,8;chunk.max_bytes=2MiB,4MiB,8MiB'
```

The objective is the geometric mean of the named cells' MEDIAN PAIRED RATIOS —
the same verdict the sweep table's ratio column carries, for the same reason,
and geometric so a proportional loss in the 10 MB cell costs what a proportional
gain in the 50 MB cell pays. A cell with no paired file falls back to that
cell's median goodput; a run that produced no cell at all (the rig dropped, the
set went INVALID) is a MISSING observation and updates nothing — never a zero,
which would retire a value on one bad night. Each round runs into
`<out>/tune/r<round>-<knob>=<value>/` with the campaign's `paired-ref.txt`
copied in, exactly as a sweep value gets it: the reference is the control and
has to be the same route in every round or the rounds are not comparable.
`-route-knobs '--ecf-max-window=4MiB,8MiB,16MiB'` puts router flags on the same
grid, through `ROUTE_SETTINGS`.

The output is `<out>/tune/tune.tsv` (round, knob, value, the settings applied,
the objective and every cell that fed it), `<out>/tune/incumbent.txt` (a
`SETTINGS=` line to paste into the campaign) and a table per knob: value, n,
mean, ±stderr, and which value the posterior currently calls best. Read n first
— a bandit deliberately stops paying for a losing value, so a row with n=1 has
not been ruled out, it has been given less of the budget, and only the arms with n≥3 and a
stderr well inside the gap between them have said anything. The incumbent is a
place to point a campaign, not a verdict: the verdict is still a full run under
`bench/verdict.sh`.

## 3. The work, in order, each step judged by the rig

### 3.1 Bound what is in flight per leg

The memory of the last campaign names this "defect C": `wrapPayload` accepts
unbounded in-flight, so a slow leg is fed until its queue is seconds deep and
the frontier waits on it. This is the first change because it is the one the
anti-aggregation numbers point at directly.

- A per-leg send window: in-flight bytes on a leg ≤ its baseline-RTT BDP,
  from the delivery rate the receiver's SACKs prove, not from bytes handed to
  the transport (ECF's `rateBps` still counts the latter).
- When every leg is at its window the writer blocks; it never queues into a
  bloated leg.
- Control frames (SACK, pong, leg-state) are sent ahead of data on every leg.
  The false liveness teardown is a pong stuck behind data; this removes the
  cause rather than tolerating it.

Rig question after this step: does the download row stop being monotone
decreasing? If 2 legs ≥ 1 leg but 6 < 4, the window is right and the
scheduler is next. If 2 < 1 still, the frontier is next (§3.3).

### 3.2 Make the scheduler predict arrival, not throughput

ECF picks the leg that completes soonest; it is only as good as its inputs.

- Delivery-proven rate per leg (from 3.1) and per-leg end-to-end latency
  EWMA (#3985) feed the completion estimate; transport-level RTT is not used.
- Cold legs get the probe budget and ramp; a leg whose delivery rate falls
  below a fraction of its baseline is shed, not fed.
- Live tuning through `proxy mux mode|cap|width` on the rig, one knob per
  run; the constants that win go in as defaults with the numbers that chose
  them.

Rig question: at 4 and 6 legs, is the exit's per-leg `sent_bytes` share
within tolerance of each leg's measured capacity share? If shares are right
and goodput is still below the sum, the frontier is the remaining loss.

### 3.3 The frontier: stop waiting on the slowest leg

Only if 3.1 and 3.2 leave anti-aggregation on the table. Two candidates,
both capability-negotiated, no flag:

- **Per-leg sequencing with a stream-layer merge:** each leg carries its own
  ordered sub-stream; the receiver keeps a cursor per leg and reassembles by
  a global order only at stripe boundaries, so one leg's lag delays one
  stripe, not every later packet. This is what MPTCP does with its two
  sequence spaces.
- **Aggregation above the route group** for what can be split: the HTTPS
  range-split path already does this and is the one place multi-leg beats
  single-leg in production today.

The rig decides between them by the same table. Neither is started until
3.1 and 3.2 have been measured, because the last campaign spent weeks on the
wrong layer.

### 3.4 Liveness and stability, finished

- With control-frame priority in place, re-run the frozen-exit 30-minute
  continuous-load sample that measured 13/30 then 6/30 collapses. Target 0.
- Every promote, park, prune and heal decision writes its reason into
  `visor state` and `proxy mux info`; a run's churn is reported as a count
  with reasons, and the bound in §1 is enforced against it.
- A manual pin (`proxy mux set`, `--route`, `--routing-policy none`) is
  respected by the adaptive growth. It was not, live.

### 3.5 Direction, proven

Both ends on the same build, download-heavy and upload-heavy runs: the
direct leg carries one direction and the mux legs the other, the flip
happens at the stated ratio and cools down as specified. This was
implemented and never demonstrated.

### 3.6 The default, decided by the table

Adaptive ships as the default only when it wins §1 on the rig, and it must
choose a single route on a path where a single route wins. Until then the
default is whatever the table says, which today is a single route.

## 4. Milestones

1. Baseline table on v1.3.94 (both directions, four sizes, 1/2/4/6 legs).
2. 3.1 merged and deployed both ends; table re-run. Decision: scheduler or
   frontier next.
3. 3.2 tuned live; table re-run.
4. 3.3 if still needed; table re-run.
5. 3.4 and 3.5; 30-minute stability sample at 0 collapses.
6. 3.6: the default chosen by numbers; release notes carry the table.

Rule from here on: no mux change merges without a before/after row from the
rig, and no live-found bug is fixed without a reproduction (appendix) that
stays as its regression.

## Appendix: the safety net (not the work)

- **Deterministic legs in `pkg/router`:** a `netsim` with latency, jitter,
  bandwidth, stall, black-hole, death and RTT-ramp schedules behind one
  injectable clock, two real route groups end to end; one scenario per bug
  the campaign has found, plus a seeded randomized long-run under `-race`
  asserting integrity, liveness, no anti-aggregation (tagged `muxgoal`,
  expected to fail until §3.3 lands), amplification and stability.
- **Control plane in process:** concurrent leg setup (#80 class), pin
  respected, same-LAN filters, dial contention.
- **Docker lane with `tc netem`:** four visors, ≥2 disjoint legs, the
  loadtest driver, invariants with CI tolerances; non-required until a week
  without a flake.
- **The benchmark script:** §2 as one command, run before every release and
  nightly against the last release's table.

## Results (2026-09-16/17 live campaign)

Twenty campaigns ran over two days, `bench/2026-09-16/<commit>/`, one README per campaign;
`bench/2026-09-16/a5b973a97/` is the closing full suite — the first run in which the tunnels sets
were dialed by the **shipping default** rather than by hand. The campaign's scoreboard expands §1's
five criteria into the eight below.

### The rig and the method

Everything was measured on one frozen rig: this host (US) as the client, one controlled exit in
Frankfurt (`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`), and six
single-intermediate stcpr routes to it whose first hops sit on six distinct /24s in five distinct
/16s, none sharing a host or a NIC with another (`bench/2026-09-16/intermediates.md`). Every campaign
ran both ends on the same commit against a reference set measured on that same commit within the
same two hours — eight single-route references, five transfers per size and direction, each
hash-verified — because the bars drift by up to 2x over twelve hours. From campaign15 on, every mux
PR also got a two-trial smoke run before merge; that caught two regressions (#4972's first branch,
#4977) that would otherwise have cost a full campaign each.

### The ceilings

The campaign's last day was spent establishing what the numbers are allowed to be:

| measurement | down MB/s | up MB/s |
|---|---|---|
| this host's raw link (Cloudflare, four parallel; 11.4 single) | 12.1 | – |
| skywire, two independent single routes at once (`run-capcheck.sh`) | 10.41 | 13.95 |
| skywire, best single route (`8170476dd/ref-via-03f57e7c`, `ref-via-0371ab4b`) | 8.84 | 9.99 |
| one range-split object, steady state | 9.1–9.7 | – |
| the bench sink, measured locally on the exit | 205 | – |

A range-split download fits `t = a + b·S` with a fixed prelude `a ≈ 1.1 s` (exit round trips before
the parallel chunk fetches start) and `1/b = 9.1–9.7 MB/s`; the 8.x medians are that steady rate
amortised over the prelude, which is 17 % of a 50 MB transfer and much more of a 10 MB one. It is
not CPU: per-thread sampling on both ends peaks at 10.5 % of a core, the transport RX loop at 9 %
while moving 10 MB/s. So the honest ceiling for one object on this host is ~9.5, for the path
10.4 down / 13.9 up, under a 12.1 MB/s link.

### The closing run — campaign20, a5b973a97, no pins on the tunnels sets

`bench/2026-09-16/a5b973a97/`, both ends at a5b973a97 (#4981 two tunnels by default + sibling dialed
on the best-ranked unused route, #4982 sink hash cache + optimistic CONNECT, #4983 full-path latency
ranking + LAN exclusion). The tunnels sets were auto-dialed by the shipping default; legs and compose
were pinned. Bars are the eight-set reference suite measured into the same directory right after the
mux sets, because the drift probe came back past 25 % on two cells in opposite directions (direct
stcpr 50 MB down 2.11 vs its own 5.11 reference, Atlanta 50 MB down 10.06 vs 6.09).

Bars: **10 down 5.00, 10 up 7.52, 50 up 10.32** direct stcpr; **50 down 5.43** via
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`. 120/120 mux rows and 160/160
reference rows hash-verified; no reorder wedge on either end in the whole run.

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| tunnels-2 (auto) | 4.54 | 7.25 (96 %) | **7.37 PASS** | 9.07 (88 %) |
| tunnels-3 (auto) | 4.71 | 5.57 | **7.00 PASS** | 9.11 |
| legs-2 | **5.33 PASS** | 4.64 | **7.05 PASS** | 8.81 |
| legs-3 | **5.14 PASS** | 5.09 | 6.05 (w/g **1.36**) | 8.67 |
| compose-T2xL2 (2x2) | 3.47 | 4.53 | **6.51 PASS** | 8.94 |
| compose-T2xL1 (2x1) | 4.61 | 4.69 | **8.33 PASS** | 8.73 |

#4983 did what it was merged for: the auto-dial excluded the same-LAN first hop that wrecked
campaign19 by name, ranked 35 candidates by first-hop *plus* second-hop latency and chose Atlanta
`95839ad0`; tunnels-3's second decision ranked 164 and chose Frankfurt `f1012467`. Both are routes an
operator would have pinned. tunnels-2 stripes 50 MB downloads 58/42–75/25 across its two tunnels at
w/g 1.01; its uploads ride one tunnel whole, which is why every upload cell is a single-route number
with mux overhead on it.

### The criteria

1. **Refs — MET.** Eight single-route reference sets per campaign, hash-verified, wire/goodput
   1.00–1.02; the closing bars are `bench/2026-09-16/a5b973a97/ref-*.tsv` and every set's hop list
   is recorded in its `.legs.json`.
2. **Tunnels ≥ best ref, 5/5 — PARTLY: MET on the 50 MB download, not on 10 MB down or 50 MB up.**
   In the closing run the auto-dialed tunnels-2 passes the 50 MB download bar, 7.37 vs 5.43, w/g
   1.01, 5/5 hashes. It reaches **96 %** of the direct uplink bar on 10 MB up (7.25 vs 7.52) and
   88 % on 50 MB up (9.07 vs 10.32), and 91 % on 10 MB down (4.54 vs 5.00). Earlier runs pass
   uploads outright when the bars are lower (10 MB up 6.64–6.82 vs 5.97; 50 MB up 10.00 vs 9.99).
3. **Legs ≥ best ref, port constant — PARTLY: MET on the 50 MB download with two legs.** legs-2
   passes both download cells in the closing run (10 MB 5.33 vs 5.00, 50 MB 7.05 vs 5.43), and
   passed earlier at 8.46 vs 8.05 (4cc8e9b6b) and 5.71 vs 5.51 (52e430fff). **A third leg hurts**:
   legs-3 drops to 6.05 on the 50 MB download and is the run's only amplification failure. The
   group's dst_port was constant for every set of every campaign, but run-to-run variance is ±20 %
   on the same pins and the same scheduler.
4. **Composition ≥ each component — NOT MET.** Two legs inside each of two tunnels never beat either
   alone: 5.63 (1ce356b2d), 6.67 (4cc8e9b6b), 7.15 (8170476dd), 7.11 (a5b611278), 6.51 (a5b973a97)
   on the 50 MB download, against 7.05–8.46 for the best single-shape cells of the same runs.
5. **Direction, from both ends — PARTLY.** Both ends' per-leg counters are captured per row since
   campaign17 (`<set>.exit-recovery.tsv` beside our own `.recovery.tsv`) — 119 of 120 rows in the
   closing run, the miss a 40 s exit-snapshot timeout — so attribution is measured rather than
   inferred. Forward traffic rides the direct or lowest-RTT tunnel by policy (#4972, #4978): in the
   closing tunnels-2 set every upload object rode one tunnel whole, while its downloads fanned
   58/42–75/25 across both. The automatic flip on load was not demonstrated.
6. **Degradation — MET for legs, NOT MET for tunnels.** Cutting a leg mid-transfer
   (`proxy mux rm`, 50 MB, 3 trials): the port stays constant, no group is rebuilt, hashes 3/3 both
   directions, ttfb after the cut 0.9 s down / 1.7 s up, reorder wedge 1.5–1.9 s cleared in 0.5 s.
   Cutting a tunnel's transport closes its group, because a tunnel is a single-leg group: downloads
   survive at 1.1 MB/s with 35–40 s to resume, and a POST in flight on the cut tunnel cannot be
   resumed at all.
7. **Does not amplify, bounded named churn — MET except legs-3's 50 MB download.** In the closing
   run wire/goodput is 1.00–1.12 in every cell but one: legs-3 50 MB down at **1.36**, the run's
   only amplification failure and the reason that cell does not pass despite clearing the rate bar.
   Every park and promote carries a named reason (shared bottleneck, latency band with its min-RTT,
   peer-mirrored, operator), and the 30 s park hold from #4968 bounds the churn: 5–26 events per
   20-row set in the closing run, with **no reorder wedge on either end**.
8. **The default, by the table — SHIPPED.** `tunnels=2` is the shipping default
   (`skyenv.SkysocksClientTunnels`), with the sibling dialed on the best-ranked unused route (#4981)
   ranked by first-hop *plus* second-hop path latency and with same-LAN first hops excluded (#4983).
   Measured with no pins at all in campaign20: 50 MB down 7.37 vs the 5.43 bar, w/g 1.01, 5/5
   hashes; 96 % of the direct uplink bar on 10 MB up, 88 % on 50 MB up. The auto-dial picked Atlanta
   and Frankfurt by itself — the routes an operator would have pinned — where campaign19's
   first-hop-only ranking picked a 1 ms LAN neighbour and collapsed. A third tunnel buys nothing.

### v4 (2026-09-18) — the criteria as re-worded

The eight criteria above are the 2026-09-16/17 scoreboard and stand as they were
measured; nothing in them is rewritten here. These rows re-word two of them
against the **endpoint ceiling measured in the same campaign**
(`bench/run-ceiling.sh`, §1) and add a tenth:

| criterion | v4 (2026-09-18) |
|---|---|
| 2 — two concurrent uploads | "≥ the smaller of (sum of the two best references) and 0.95 × the measured uplink ceiling, the ceiling measured in the same campaign as concurrent direct uploads to the exit, sink-verified" |
| 4 — 50/100 MB composition | "≥ the better of the two alone when that better one does not saturate the endpoint (its rate < 0.9 × the measured endpoint ceiling), otherwise ≥ 0.95 × the endpoint ceiling; the ceiling measured in the same campaign" |
| 10 — spread policy (new) | "under a spread policy that caps any one route at 40 % of the bytes and holds at least three routes, throughput ≥ 0.8 × the best single reference and no route exceeds its cap, measured from both ends' per-leg counters at 50 MB down and up; the policy is a live setting" |

`bench/verdict.sh` scores 2 and 4 from `ceiling.tsv` when the campaign measured
one and says which bound bound each verdict; criterion 10 is the `mux-spread-3`
set of `bench/run-mux.sh` and its `.assert.tsv`.

### Direction (criterion 5)

`bench/direction.sh <result dir> [set…]` turns the artefacts a run already writes into the per-row
attribution criterion 5 asks for. For every row (trial × direction × size) it takes the local end's
`<set>.carrier.tsv` deltas — `sent_delta` is what this visor put on the leg (client→exit, forward),
`recv_delta` is what arrived on it (exit→client, reverse) — together with the `legs` pseudo-row that
names the rg:transport membership at that row. Leg identity comes from `<set>.legs.json`:
`transport_id`, `tp_type`, `remote_pk`, `latency_ms`, `direct`, `hops[]` and `tunnel_role`, with
`<set>.tps.tsv` as the fallback for a group dialed after the snapshot. The exit end is
`<set>.exit-recovery.tsv`, matched to our legs through `hops[-1].tp_id` — the exit's own first hop
for that leg — and contributing `standby`, `retransmits` and `ack_delay_ms`. The exit's per-leg
record carries **no byte counter**, so the exit's sent bytes are read as the local `recv_delta` of
the same leg; two further limits are printed as `# note` lines in every output — `carrier.tsv`
counts a transport rather than a route group, and `latency_ms` is the end-of-set snapshot. Scope is
decided by bytes carried, not by the set-start snapshot: every leg of an active group counts, and so
does any leg holding ≥ 1 % of that row's payload-direction bytes, because a tunnel the pool promotes
mid-set carries payload for rows whose `tunnel_role` still reads `standby` — on `mux-spread-3` of
`bench/2026-09-16/f9107c982-smoke` that is the difference between scoring row 1 over 2 legs at a
0.669 max share and over the 4 legs that actually carried it at 0.482. Per row it reports `forward_on` (the leg with ≥ 80 % of the forward bytes, and whether that
leg is the direct or the lowest-latency one), `reverse_fanout` (legs above 10 % of the reverse
bytes, and the largest single share) and `flip` (a change of dominant forward leg between rows). It
writes `<set>.direction.tsv` per set and a `direction.tsv` summary at the top of the result dir.

Run over the four smoke dirs of 2026-09-16 (`2ca6cf7b3`, `0290ff96a`, `0186db249`, `deac6fa5a`) the
shape is consistent. Where a direct one-hop leg exists, forward takes it or takes the lowest-RTT leg
and never anything else: `mux-tunnels-2` scores 5/10, 9/10, 6/10 and 10/10 rows PASS with **no FAIL**
in any run, and `mux-standby-8` 7, 8 and 11 of 12, always on `b414796d`, the one-hop leg to the exit.
`mux-legs-2` is the counter-case and the finding worth acting on: its two legs are both two-hop and
neither is direct, and forward settles on `fdab37dd` at ~147–149 ms rather than on `95839ad0` at
36–132 ms — 7, 7, 6 and 2 rows of 10 FAIL on that account, the remainder split between rows where
the lower-RTT leg did win and rows where forward fanned below the 80 % bar. Reverse does fan: on
downloads of ≥ 50 MB two legs clear 10 % in three of the four dirs (2/2 PASS), and 6/6 in
`mux-compose-T2xL2`, where four active legs let the reverse stream spread up to four ways; it
collapses onto one leg in `0290ff96a`'s tunnels and standby sets. The dominant forward leg does move
within a set — 0 to 5 flips per set, several of them between consecutive trials of one cell rather
than at a cell boundary — so the choice is not fixed for the life of a group, but nothing here
isolates load as the cause and criterion 5's "flipping on load" stays unproven.

### The fixes this campaign merged

| PR | symptom → fix |
|---|---|
| #4962 | `proxy mux info` hung under load → the window-refresh/SACK lock inversion broken; a lone leg never parks |
| #4963 | a new stream landed anywhere → it goes to the tunnel with the most proven bandwidth |
| #4964 | the exit parked healthy legs during a download → the receive-side stall detector judges legs by payload |
| #4965 | an idle tunnel's keepalive rate counted as proven capacity → busy-only sampling, stale idle tunnels re-probed |
| #4966 | three serialized mesh round trips per range chunk → one, with parallel fetches starting at once |
| #4967 | `--rg` could not name a tunnel (all groups share src_port 3) → it takes the group's own dst_port |
| #4968 | a leg flapped 13 times in 65 s → an adaptive park holds 30 s; the latency band cannot undo a bottleneck park |
| #4969 | an idle leg read as stalled, and a peer's park was silent → idle ≠ stalled, peer parks are events |
| #4970 | ECF and RACK used first-hop RTT (10 ms vs 95 ms) where feedback was 170 ms → both use end-to-end delay; the band uses windowed min-RTT |
| #4971 | a 15 s exit-open timeout surfaced only as http 000 → logged, counted, and that tunnel sits out the next picks |
| #4972 | the BDP baseline ratcheted on a 470 ms path (50 MB up 9.82 → 2.80) → the window follows feedback delay over a first-hop baseline; a lone stream takes the lowest-latency tunnel |
| #4973 | the standalone socks client could not carry over dmsg → it does, and takes the server flags |
| #4974 | a chunk on a closed tunnel waited out 35–40 s of timeouts → it fails at once and refetches on a live tunnel |
| #4975 | route groups outlived their app, so the next `--route` pin landed on a stale group → an app's groups close when it stops |
| #4976 | — bench scripts, results and the campaign's data |
| #4977 | park gate and retransmit charging reworked → measured worse twice (legs-2 50 MB down 5.86 and 6.54 vs 7.51); **closed, not merged** |
| #4978 | two concurrent streams stacked on one tunnel (>99.5 % of bytes) → the pick scores `rtt × (streams+1)` |
| #4979 | the first stream paid a serialized greeting and CONNECT → pipelined like a chunk's, one round trip off the prelude |
| #4981 | the sibling tunnel took any unused first hop → it is dialed on the best-ranked unused route, and two tunnels are the default |
| #4982 | the loadtest sink re-hashed its object per request, and the split path serialized CONNECT → hash cached once, CONNECT answered optimistically |
| #4983 | ranking by first-hop latency chose a 1 ms LAN neighbour whose second hop was never measured → full path latency, and same-LAN first hops are not diversity |

### Open

- **Per-object prelude.** ~0.9 s remains after #4979; it is the gap between one split object
  (8.2–8.5) and two concurrent objects (10.0).
- **The packet-level scheduler is left as measured.** Both #4977 variants regressed; legs-2 at
  ~7.5 is 85 % of the best single route and that is where it stands.
- **Silent all-paths stalls,** roughly one per 100 transfers, on plain routes as well as mux: every
  group freezes together for 15–55 s and the client's exit-open sniff timeout closes the connection.
  Wants a transport last-read/last-write age diagnostic.
- **10 MB downloads are the weakest cell in every campaign** — three 4 MiB chunks leave nothing to
  parallelise and the prelude is a fifth of the transfer. Only the pinned legs sets have ever
  cleared it (5.33 and 5.14 vs 5.00 in campaign20); the auto-dialed tunnels sets sit at 91 %.
- **A dmsg-only reference set** is still unmeasured; it is blocked until #4973 is deployed fleet-wide
  and measured.
- **Route volatility.** The 50 MB download bar was 8.84, then 6.63, then 5.43 in three consecutive
  measurement windows, and the direct downlink lost 59 % of itself between one campaign's references
  and the next campaign's drift probe. A ranking taken once, at dial time, is ranking against a
  number that will be wrong within the hour.

### Next phase — the standby pool

This supersedes criterion 8's earlier "needs route ranking first" decision text. The ranked pre-dial
of #4981/#4983 is an **interim step**: it made the default safe to ship and it demonstrably picks
good routes, but it commits to a ranking taken before any bytes move. The user's direction, verbatim:

> "route ranking before dialing isn't the best approach, just dial / set up the routes and hold them
> in standby, that would be the best approach and the only way to have routes that can be switched in
> in an instant."

So the next phase dials a **standby pool** — routes set up and held, not carrying traffic — and
switches on **live measurement** of the legs that are actually running, so a route that degrades
mid-transfer is replaced in an instant rather than at the next dial. Probing the pool over
dmsg-over-skynet relay legs is part of the same phase.
