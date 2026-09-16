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
