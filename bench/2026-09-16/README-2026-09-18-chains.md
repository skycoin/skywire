# Chain runs of 2026-09-18 (UTC)

The 2026-09-18 chains, continuing the index of `README-2026-09-17-chains.md` (chains 21 and
A–N). Chains O–W ran overnight against the merged-develop deploy and the first ceiling
measurements; X–AK are the day's branch runs. Each result dir carries its own README with
the verdict rows, the cut row and the exit gate; this is the index. The paired reference is
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` throughout, and from
chain AB on the verdicts are ceiling-aware against chain Z's `ceiling.tsv` (uplink 11.23,
downlink 9.55 MB/s).

| Chain | Dir / commit | What it tested | Key numbers | Outcome / PRs |
|---|---|---|---|---|
| O | `2ca6cf7b3-smoke` | merged develop: `proxy settings` #5002 + docs #5005 | tunnels-2 50 down 7.08 vs 5.43 PASS; standby-8 cut ttfb 1.139 s; 12/12 hashes | baseline deploy for the sweeps; #5002/#5005 already merged |
| P | `2ca6cf7b3-sweep/sweep` | live sweep: `upload.chunk_bytes`, `upload.concurrency` | 50 up 8.69/8.80/9.84 across 1/2/4 MiB; conc 2 vs 8 = 9.84 vs 8.87 | no value beats the default; unpaired (ref resolved to `direct`) |
| Q | `2ca6cf7b3-sweep/sweep` | live sweep: `chunk.max_bytes`, `chunk.concurrency`, `chunk.per_tunnel` | 100 down 5.85/5.71 (2/8 MiB), 7.41/5.55 (conc 4/16) | composition cap is not a chunk-knob setting |
| R | `2ca6cf7b3-sweep/route` | `route settings --ecf-max-window` 4/16 MiB, `--ecf-window-margin` 1.5/3.0 | 50 down x0.948/x0.899 (4 MiB), x1.290/x0.759 (16 MiB), x0.381/x0.712 (margin 3.0) | both changes worse than the shipped 8 MiB / margin 2 |
| S | `1647ae058-smoke` | #5008 object-sized upload chunk | 50 up x0.916; two uploads sum 8.70 vs ref 17.70, x0.495 FAIL; degrade 2/2 200 | merged as 21003f4ee; upload gap not closed |
| T | `845dd383e-smoke` | #5010 lowest-latency forward leg + exit per-leg bytes | legs-2 fwd lowest-latency 9/12 PASS (was 2/10); 50 down x1.093 PASS | merged as 48872035e — criterion 5 |
| U | `1647ae058-exitwin` | exit `--ecf-min-window` 8 KiB vs the 128 KiB default, compose 2x2 | 50 down 5.96 vs 5.68; 100 down 7.53 vs 7.78 | no effect; default kept |
| V | `845dd383e-ceiling` | first endpoint ceiling, 3 concurrent direct clients | uplink 10.70 (x1.18); downlink 5.04 (x1.07) | the direct downlink is itself the bottleneck → #5016 |
| W | `edc45929f-smoke` | #5012 v1: SBD per SACK + RACK per-leg delay | compose 50 down x0.944; legs-2 50 down 3.75; `retx_sent 119 -> 251` | not merged — the park verdict costs the second leg |
| X | `f9107c982-smoke` | int-H: #5014 spread + #5013 snub/depth | every row NOBAR — three stub pins invalidated the paired refs; spread knobs landed in 8 s, `max_share 0.6690` | not scoreable; produced the pin-validation and unfiltered-log rules (#5019) |
| Y | `f9107c982-tune` | 12-round Thompson tuner over the upload/chunk knobs | incumbent `upload.chunk_bytes=4MiB upload.concurrency=2 chunk.max_bytes=2MiB` | the incumbent is the default; tuning does not reach the upload gap |
| Z | `f9107c982-ceiling2` | #5016 downlink ceiling over the 3 best distinct routes | uplink 11.23 (x1.14), downlink 9.55 (x2.12); via-0371ab4b uplink 10.30 > direct 9.85 | the ceilings every later chain is scored against |
| AA | `03ece1e95-smoke` | int-I: #5018 intake worker + #5014 + #5013, valid pins | tunnels-2 50 down x0.850; spread `routes_active 3–4 PASS`, `max_share 0.9580 FAIL`; cut ttfb **6.784 s FAIL** | #5018 merged as a50f1517e; the cut cost a whole chunk → #5023 |
| AB | `195b1094c-smoke` | int-J: + #5012 SBD-per-SACK, first ceiling-aware verdict | tunnels-2 50 down x1.182, 50 up x1.023 PASS; legs-2 50 down x0.777; compose 100 down x0.858 | #5012 not merged — legs-2 still short |
| AC | `0cbbf925a-smoke` | int-K: #5023 frontier streaming + #5022 audition + #5014 + #5013 | cut ttfb **0.643 s PASS**; spread 3 routes, `max_share 0.4410 PASS`, x0.866/x0.951; tunnels-2 50 down x1.106, 50 up x1.037 | merged — #5023 8a8b1daf9, #5022 5505f18a7, #5014 5fec4edb4, #5013 8e88caf88 |
| AD | `195b1094c-sbdsweep` | live two-arm sweep: SBD off (`--sbd-min-samples 1000000`) vs default | legs-2 50 down **x1.130 PASS off / x0.862 FAIL default**, 1 vs 2 park events | the ruling: two legs do aggregate; #5012's park is the cost |
| AE | `0251e5da4-smoke` | int-K2: #5012 amended, park as a goodput trial | all four tunnels-2 cells PASS; compose 100 down x0.986 PASS; 2 upload hashes lost to the short-chunk 400 | #5012 not merged; the 400 finding → #5025 (51c2f2844) and #5027 |
| AF | `dfb0755c2-smoke` | int-L: #5012 at 0512a6044, per-SACK + park-as-trial + evidence floor | tunnels-2 50 down x1.126, 50 up x1.020 PASS; legs-2 50 down x0.703; compose 17/21 hashes, 50 up 0/3 | not merged — reworked again; #5027 written for the retry |
| AG | — | full paired campaign on merged develop (goal v4): refs, ceiling, tunnels-2, legs-2, compose 2x2 + 100 MB, standby with no pins, spread, direction | — | running: on develop `1008cc8e5`, results in `bench/2026-09-18/` |
| AH | `3535e671b-smoke` (int-M) | #5027 short-chunk retry + #5012 reworked (SBD demote off by default, per-leg sampling) | tunnels-2 50 down x1.003 / up x0.930; legs-2 50 down x1.108 PASS; compose 10 up x0.057, 50 up x0.120; 52/53 hashes | #5027 merged as `0db22347b`; #5012 held back again |
| AI | `e128db1ff-smoke` (int-N) | #5029 snub gating + #5030 acceptor park + #5012 gated; second compose arm with homogeneous-latency pins in `homog/` | tunnels-2 50 down x1.094 / up x0.992 PASS; legs-2 50 down x0.999 PASS; compose 50 down x1.001, 50 up x0.338; homog 100 down x0.613 — not a pin artefact; 33/33 hashes | #5029 `1545f15f0`, #5030 `14a8b5354`, #5012 `da1709895` |
| AJ | `6c8029830-smoke` (int-O) | #5031 upload placement by tx capacity + #5032 SACK retransmit on the leg basis | compose 100 down x1.055 PASS; 50 up x0.244, 10 up x0.041 (the day's floor); every tunnels-2 cell short; 53/53 hashes | #5031 `15e12e619`, #5032 `d23f8b3d6` |
| AK | `908a98bcd-smoke` (int-P) | #5035 forward confinement holds for uploads — a full window waits instead of spilling, with hysteresis | tunnels-2 50 down x1.075 / up x0.994 PASS; legs-2 50 down x1.084 PASS; compose 50 up 8.40 x0.816 (was 2.43), 10 up 4.60 (was 0.27); 53/53 hashes | #5035 merged as `1008cc8e5` |

Of the branches carried through chains W–AK, chain AC's four PRs, chain T's #5010 and chain
AA's #5018 merged, and chains AH–AK merged #5027, #5029, #5030, #5012, #5031, #5032 and #5035.
#5012 ran five times (W, AB, AE, AF, AH) before it merged from chain AI as `da1709895`. Chain AK's
head `1008cc8e5` is the develop the criterion status in `docs/design/route-multiplexing-test-plan.md`
is scored at; chain AG's full paired campaign is running against it.
