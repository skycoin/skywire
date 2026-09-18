# Emulated multipath testbed

Every mux failure of the 2026-09 campaign cost an hour of live rig time to
reproduce once. The emulated testbed reproduces the same shapes in seconds,
deterministically, under `go test` — no root, no rig, no deployment.

```
go test ./pkg/router/emu/ -count=1              # the emulator itself
go test ./pkg/router/ -run Emu -count=1 -v      # the scenarios (~20 s)
go test ./pkg/router/ -tags emulong -run EmuLong -count=1 -v
```

## What it emulates

`pkg/router/emu` is a pair of connected endpoints satisfying
`network.Transport` — what a `transport.ManagedTransport` wraps, so the router
cannot tell one from a real leg. Each DIRECTION has its own knobs, all settable
while the link is running:

| knob | what it models |
| --- | --- |
| `Delay`, `Jitter` | propagation delay; jitter is uniform in ±Jitter |
| `LossPct` | random frame loss. A lost frame still costs queue and rate |
| `RateBps` | link rate. Frames are SERIALIZED, so a rate limit produces real queueing delay rather than a per-frame sleep |
| `QueueBytes` | egress buffer depth. A full queue BLOCKS the writer — the backpressure a full socket buffer applies. Queueing delay is `QueueBytes/RateBps`, which is how a "60 KB/s leg with 9 s of queueing" is expressed |
| `ReorderPct`, `ReorderDelay` | a frame held an extra delay so it lands behind ones sent after it |
| `Cut()` / `Restore()` | the direction black-holes everything, in-flight frames included, WITHOUT closing the socket — a leg that dies while its conn stays up |
| `Seed` | the drop and reorder decisions are a function of the seed, so a loss run reproduces frame for frame |

Time is real, not virtual. Scenarios scale rates and transfer sizes down until
a run takes a second or two; nothing in the router has an injectable clock, and
introducing one for this would have been a far larger change than the testbed.

`pkg/router/harness_emu_test.go` stands two `RouteGroup`s up and joins them by
N emulated legs. It runs the REAL send path (`RouteGroup.Write` →
`nextTransport` → `selectTransport` → `ManagedTransport.WritePacket`) and the
REAL receive path (`handlePacket` → `serveIntake` → `handleDataPacket` →
reorder / SACK / RACK / TLP), with the mux service loops (SACK, TLP,
send-window refresh, reorder-stall, optionally leg-liveness and data-progress)
running as they do live. ECF placement, the per-leg send windows, HoL
retransmit, the unidirectional confinement, park/promote and the probe-only
ruling of #5037 are therefore all under test.

The harness lives in package `router` because a leg is an
`(rg.tps, rg.fwd, rg.rvs, rg.mux)` tuple and none of those are exported.
`pkg/router/emu` holds everything that needs no package internals: the wire and
the report format.

## What it cannot do

- **No transport handshakes, no discovery, no NAT, no dmsg, no encryption.**
  The two ends are handed to each other. Anything that only breaks during leg
  ESTABLISHMENT — a setup-node dial, a rule the peer never registered, an ALPN
  collision — is out of scope; the legs here are established before the first
  byte.
- **A leg is a datagram link, not a byte stream.** One `Write` is one frame and
  one `Read` returns that frame. The router writes exactly one `routing.Packet`
  per `Write`, so packet boundaries are preserved — and that is what lets a
  direction drop or reorder an individual packet. Real stcpr/dmsg legs are
  TCP-like: they never reorder and never lose a frame without also failing the
  conn. Loss and reorder here stand in for what a multi-hop ROUTE does between
  the two visors, not for what the first hop does.
- **No intermediaries.** A leg is one pipe. Multi-hop path effects (a shared
  bottleneck two legs traverse, an intermediary's own queue) are expressed as
  the leg's shape, not simulated.
- **No absolute throughput claims.** Everything is scaled and runs against Go's
  timers on a loaded machine. Assert ratios and properties, never a rate.
- **No router, no transport manager, no setup node.** Self-heal, rotation and
  route rebuilding need hooks the harness deliberately leaves nil, so a
  scenario can assert "the group did NOT rebuild" but cannot exercise a
  rebuild.

## Scenarios, and their state on develop

`pkg/router/scenarios_emu_test.go`, one function per live finding. All print
the same summary block: goodput, wire/goodput, per-leg sent/recv/payload
shares, retransmits, reorder drops, SACK counts, TTFB and the probe rulings.

| scenario | shape | state on e897dbfbb |
| --- | --- | --- |
| `TestEmuDeadLegShareIsBounded` | 7 MB/s / 45 ms beside 60 KB/s with ~1 s of queueing | passes as written — completes, wire/goodput 1.01, slow leg 3 % of the payload — but the group runs at **x0.26 of the good leg alone** |
| `TestEmuHealthySkewStillAggregates` | 44 ms and 166 ms at equal rates | passes: x1.57, neither leg ruled probe-only |
| `TestEmuLossBurstKeepsHashesAndBoundsWire` | 3 % loss on one of two legs | passes: hash ok, wire/goodput 1.11 |
| `TestEmuCutOfBusiestLegCompletes` | 3 legs, busiest cut a third of the way in | passes: completes, resumes ~5 ms after the cut, no rebuild |
| `TestEmuFlappingLegDoesNotWedge` | 3 legs, one cut/restored every 500 ms | passes: completes intact |
| `TestEmuUploadConfinementHoldsOnLowestLatencyLeg` | CapUniDir upload over 30/90/200 ms legs | passes: 100 % on the 30 ms leg |
| `TestEmuLongDeadLegDoesNotDragTheGroup` (`-tags emulong`) | the live shape: 60 KB/s with ~9 s of queueing | **FAILS** — see below |

### The open finding

The outclassed-leg gate (#5037) does not catch a deeply queued leg. At the live
shape the 8 MB download does not finish inside 90 s (6.5 MB delivered,
wire/goodput 1.94) against 1.17 s for the good leg alone, and the leg is never
ruled probe-only. Both halves of the ruling miss it for the same reason — the
leg is queued so deep that it never acknowledges anything:

- its DELAY basis stays at the first-hop 45 ms, because the send→ack term never
  gets a sample, so the delay half reads it as the *fastest* leg in the group;
- `delivKnown` stays false, so the goodput half declines to rule what looks like
  a merely cold leg.

The shallower-queue variant shows the other end of the same hole: there the leg
does ack, reading 1700 ms against the good leg's 350 ms — under the 6x the
delay half needs, because both bases are send→ack and inflate together — while
the goodput readings are 6.4 MB/s against 65 KB/s, a ratio of 98. The goodput
half alone would rule it in both cases; the delay half vetoes.

## Adding a scenario

1. Take the shape from the bench row that produced the finding. The carrier
   rows (`<set>.carrier.tsv`) give per-leg rates; `<set>.mux_events.json` and
   the sbd rulings give the delay bases; `<set>.legs.json` gives the per-leg
   counters.
2. Express each leg with `symmetric(name, rateBps, rtt, queueBytes)`, or build
   an `emuLegSpec` by hand when the two directions differ. A leg that is slow
   because it QUEUES is `RateBps` small and `QueueBytes` = rate × the delay the
   live detector read.
3. Scale rates and the transfer down until the run is a couple of seconds. Keep
   the RATIOS from the live row — they are what the scheduler reasons over.
4. Assert the property, never the number: a ratio against a one-leg baseline
   (`emuBaseline`), a share bound, `HashOK`, a wire/goodput ceiling, a TTFB.
5. `t.Log(s.Table())` always, pass or fail. The table is the artifact.
6. Over ~60 s of runtime, put it in `scenarios_emulong_test.go` behind the
   `emulong` build tag.

## Turning a live finding into a scenario

The campaign loop was: see a bad bench row, guess, deploy, re-run the rig, wait
an hour. The loop this replaces it with:

1. A bench row is bad. Read its carrier/legs/events files for the leg shapes
   and the readings the group's own detectors had.
2. Write the scenario above. If it reproduces, the bug is in the router and the
   test is now the iteration loop — seconds, deterministic, and it stays as the
   regression guard.
3. If it does NOT reproduce, the cause is in something the testbed leaves out
   (leg establishment, an intermediary, the transport layer, the exit host) —
   which is itself a result worth having before spending rig time.
4. Ship the fix with the scenario in the same PR.
