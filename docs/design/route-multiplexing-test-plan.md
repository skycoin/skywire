# Route multiplexing: how it gets tested to perfection

Status: plan, 2026-09-15. To be taken up after v1.3.94.

## Why this plan exists

Every serious mux defect to date was found live, on the fleet, by a person
watching a transfer wedge, and none was caught first by a test. The list is
the test plan's real specification:

| Found live | Root cause | Fix |
|---|---|---|
| download never completes, ~7× wire amplification | reorder `flushAll` **skipped a gap**; the mux carries a noise-AEAD stream, so a skip is corruption | #4005 no-skip, 2048 window |
| 93% of packets are retransmits, stream torn down | RACK ceiling clamped at 1.5 s under bufferbloat; SACK re-selected the same hole every 25 ms with no backoff | #4399 per-seq backoff, RTT-floored ceiling |
| aux leg appended, 0 bytes, route group rebuilt every ~2 s (#80) | responder init-window race: a leg arriving during the primary handshake was deleted or blocked | #4116, #4131 park the leg by descriptor |
| app `Running`, 0 bytes forever | a lone leg was never probed and never replaced | #4247 sole-leg probe + heal |
| more legs = strictly less throughput | same-LAN "2 ms" artifact legs black-hole bulk data | #4253 reject same-LAN intermediates |
| ECF feeds the stalling leg more | BDP from congestion-inflated RTT; cold legs treated as unlimited | #4254 baseline-RTT BDP, congestion shed, cold budget |
| pool grows to 100 legs, collapses to 0, grows back | in-band yamux pong HoL-blocked behind a reorder gap → false liveness teardown | #4346 late-pong tolerance (partial) |
| adaptive default stalls 10 MB transfers | `adaptStandbyMax` drifted between source and the committed wasm bundle | #4109 |
| RPC over skynet stalls | `RouteGroup.Write` did not segment > 64 KiB | #4701 |
| **still open:** upload and download both anti-aggregate, monotonically, with healthy legs | the global-sequence **no-skip reorder frontier**: one lagging leg stalls the whole group | architectural; see §6 |

Two things follow. The mux has a small set of invariants that were never
written down as assertions, and the layer where the bugs live, the data plane
under skewed and failing legs, has no test that can produce skew or failure.
The unit tests exercise reassembly with hand-ordered packets; the docker e2e
and the native e2e never form a mux at all.

## 1. Invariants, stated once, asserted everywhere

Every tier below asserts the same five properties. A tier that cannot assert
one of them says so.

1. **Integrity.** The delivered byte stream equals the sent byte stream. Not
   "mostly": the stream carries a noise cipher, so one skipped or duplicated
   byte is a permanent wedge. Assert by hash of the whole stream at the far
   end, and by the reorder buffer's own `NextSeq` never advancing past a hole.
2. **Liveness.** With at least one live leg, delivery progresses. A gap at the
   frontier is closed within one retransmit round trip of the fastest live leg,
   or, when the leg holding it is dead, within the prune delay. A stall longer
   than that with a live leg present is a defect, whatever the throughput.
3. **No anti-aggregation.** Goodput with N legs ≥ goodput of the best single
   leg, minus a stated tolerance. Anything else means the scheduler or the
   frontier is subtracting capacity. This is the invariant that fails today
   under load (§6) and it stays a test, marked as a goal, until it passes.
4. **Bounded amplification.** Wire bytes / goodput bytes ≤ 1.2 in steady state
   with no loss; retransmits are never re-sent to the same hole faster than the
   backoff allows.
5. **Stability.** The leg set changes only for a cause the visor can name
   (latency band, liveness, operator). The route group's local port never
   changes during a transfer, because a changed port is a rebuild, not a
   reshape. Leg churn per minute is a reported, bounded number.

The reasons a leg was promoted, parked or pruned already exist as decisions
in the code; they become a per-leg `reason` field in `visor state` and
`proxy mux info` so a failed assertion prints why, not just what.

## 2. Tier 1: deterministic leg simulation in `pkg/router`

The gap that matters most. Today `route_mux_e2e_test.go` hands packets to
`deliverData` in an order the test chose. What is needed is legs that behave
like the network did when each bug happened, under a clock the test controls.

**Harness.** A `netsim` package under `pkg/router/internal` providing:

- a simulated leg: in-memory pipe with configurable one-way latency, jitter,
  bandwidth (token bucket), and a schedule of events: stall for D, black-hole
  from T, die at T, RTT ramp (bufferbloat), reorder within the leg, duplicate;
- a virtual clock. The mux reads `time.Now` and starts timers in `sack.go`,
  `reorder.go`, `route_group.go` (liveness, data-progress cadence, ECF refresh)
  and `transport_selector.go`. These move behind one injectable clock so a
  30-minute scenario runs in seconds and every timing threshold is exact
  (`livenessProbeInterval` is already a var; the rest follow the same pattern);
- two real `RouteGroup`s wired end to end over N simulated legs, with the real
  routeMux, reorder buffer, SACK and scheduler in the path, driven by a real
  byte source and sink so integrity is checked on the whole stream.

**Scenarios,** table-driven, seeded, each a regression for a row of the table
above and each asserting all five invariants:

- two legs, 40 ms and 245 ms, no loss: aggregation, no stall (the ECF
  hold-back case);
- one slow leg in a fast set: share tracks capacity, frontier never waits
  longer than one fastest-leg RTT for the slow leg's stragglers;
- a leg black-holes mid-transfer: prune within the liveness window, stream
  continues, no rebuild;
- a leg dies at T, another is added at T+5 s: the new leg starts cold and
  ramps; no burst on the frontier;
- RTT inflation on one leg from 130 ms to 1 s over 20 s (bufferbloat): cwnd
  does not grow with it, load sheds, no retransmit storm (assert the backoff
  and amplification bound directly);
- cold start with 6 identical legs: no single-leg dump;
- 1, 2, 4, 8, 16, 60 legs of equal capacity: throughput monotone
  non-decreasing, reorder window never the limit;
- sole leg black-holes: probe fires, replacement dialed, dead leg pruned after;
- upload and download direction separately, then both with `CapUniDir`;
- FEC on and off across every scenario with loss > 0;
- write sizes 1 B to 1 MiB, including the > 64 KiB segmentation;
- a randomized long-run: 10⁴ events drawn from all of the above with a seed
  printed on failure. Runs under `-race`. Integrity must never fail; the other
  invariants log the first violation with the seed.

**Scheduler conformance**, separately, because it is pure: given legs with
known capacity and RTT, `ECF`, `equal` and `capacity` weighting produce shares
within a stated tolerance of the ideal, at cold start and at steady state.

Size: the harness is the bulk of the work, roughly a week; the scenarios are
a day each once it exists. Everything in this tier is a normal `go test` and
gates every PR.

## 3. Tier 2: control plane, in process

`router_mux80_test.go` and the IntroduceRules tests exist; they grow into a
harness of several real routers connected by simulated transports, with
induced handshake delay and failure, asserting:

- N disjoint legs stand up concurrently without a rebuild (the #80 class),
  with the init-window race reproduced on purpose;
- standby, promote, demote and self-heal decisions match the policy's tick
  inputs, and every decision carries its reason;
- a manual pin (`proxy mux set`, `--route`, `--routing-policy none`) is
  respected: the adaptive growth never fights it (this was broken live);
- same-LAN and hop-exclusion filters reject what they should;
- route-setup contention: several clients dialing one destination at once
  each get a route, or fail with a named reason, never a silent 1-leg fallback
  (the degrade `mux-route-probe.sh` was written to catch).

## 4. Tier 3: multi-visor CI lane with a shaped network

The docker e2e harness already runs several visors. It gains one scenario:
four visors, a mux of at least two disjoint legs, and `tc netem` on the
container links so each leg has its own latency, jitter and loss. The exit
runs `proxy loadtest serve`; the client runs `proxy loadtest run` and
`proxy mux info --ndjson`; the test reads the NDJSON and asserts the five
invariants with CI-sized tolerances, plus `rg ls` leg count against what was
asked for (the silent N→1 fallout). Kill one leg's container mid-run and
assert the stream survives. Roughly two days on top of Tier 1's assertions,
which it reuses. Starts non-required and becomes required after a week
without a flake; a flake is classified with the usual protocol, never waited
out.

## 5. Tier 4: the fleet benchmark, scripted and repeatable

Everything the live campaign learned about method, made into one script so
nobody rediscovers it:

- one controlled exit on a frozen build (auto-update timer stopped, uptime
  recorded across the run to prove no restart);
- two clients on the same node: A with one leg (the reference), B with N legs
  (the subject), dialed sequentially, each session confirmed before the next;
- no reconfiguration during a run; every `proxy mux set` / `mode` / `width`
  change is its own run;
- fixed sizes 3, 10, 50, 100 MB, five trials each, both directions, pulled
  concurrently through A and B;
- results as NDJSON in `bench/<date>/`, with `proxy mux info` sampled every
  second and the exit's `visor state --via dmsg://` before and after;
- pass: B completes 5/5, B ≥ A within tolerance, B's local port constant, and
  churn/min below the bound; the report is a table, not a log.

This runs before every release and nightly against the previous release's
numbers; a regression is a diff against last time's table.

## 6. The open architectural problem, kept honest

The measurement that ended the aggregation campaign was clean: with healthy,
converged legs, 1 leg 350 KB/s down, 4 legs 144, 6 legs 12; and upload, which
the client schedules itself, 7.1 → 5.8 → 3.5 MB/s. More legs, less
throughput, monotonically, on both sides. Send-side governance is ruled out.
The cause is the global-sequence, no-skip frontier: one lagging leg stalls
the whole group, and every added leg is another chance to lag.

The test plan does not paper over this. Invariant 3 is written as a test
now, tagged `muxgoal`, run nightly, expected to fail, with the failing numbers
published. The candidate fixes it will judge are per-leg sequencing with a
merge at the stream layer, and aggregation above the route group (the
range-split path already proves the HTTPS case). Whichever lands, the same
test passes it or it does not ship as the default.

## 7. Order of work

1. Clock injection and the `netsim` legs (Tier 1 harness).
2. One scenario per row of the table in §0, each a regression test.
3. Reason fields on leg decisions; `visor state` and `proxy mux info` carry them.
4. The randomized long-run under `-race`.
5. Tier 2 control-plane harness and the pin-respect test.
6. Tier 3 docker lane, non-required, then required.
7. Tier 4 benchmark script, first run against v1.3.94 as the baseline.
8. The `muxgoal` anti-aggregation test, and the work it judges.

Rule from here on: a mux bug found live gets its simulation reproduction
before its fix is merged, and the reproduction stays as the regression.
