# 1008cc8e5 — full paired campaign against goal text v4 (2026-09-18)

Both ends on develop `1008cc8e5` (#5035, forward confinement for uploads). Local visor
`0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c`, exit
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1` (Frankfurt), sink on the exit at
`http://127.0.0.1:18080`, proxy under test `skysocks-client` on :1080, paired reference client
`skysocks-client-ref` on :1081. Deployed 10:49:36–10:54:24Z; sets ran, in order (UTC):

| set | start | runner |
|---|---|---|
| references (8 sets) + paired probe | 10:54:24Z | `run-refs.sh` |
| endpoint ceiling | 11:38:28Z | `run-ceiling.sh` |
| tunnels-2 + up2 (**INVALID**, see `tunnels-attempt1/`) | 11:40:41Z | `run-mux.sh` |
| legs-2 | 11:44:58Z | `run-mux.sh` |
| compose 2x2, 10/50/100 MB | 11:49:26Z | `run-compose.sh` |
| standby-5 with the cut row, no pins | 12:02:04Z | `run-standby.sh` |
| spread-3 (`spread.max_share=0.4 spread.min_routes=3`) | 12:06:48Z | `run-spread.sh` |
| direction + verdict | 12:11:59Z | `direction.sh`, `verdict.sh` |
| tunnels-2 + up2 re-run into this dir after `proxy stop` | 12:12:40Z | `run-mux.sh` |
| direction + verdict, final | 12:23:50Z | `direction.sh`, `verdict.sh` |

Chain logs: `refs.chain.log`, `ceiling.chain.log`, `legs.chain.log`, `compose.chain.log`,
`standby.chain.log`, `spread.chain.log` and `tunnels.chain.log` (the re-run;
`tunnels-attempt1/tunnels.chain.log` is the first attempt).

## References and ceiling

Full reference suite, medians MB/s, 3 trials per cell (`ref-*.tsv`):

| reference | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| direct stcpr | 4.99 | 7.37 | 5.78 | 9.88 |
| direct squicr | 3.94 | 1.35 | 0.49 | 2.34 |
| via `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` (US-Atlanta) | **7.61** | 7.36 | **8.87** | **10.33** |
| via `03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf` (NL) | 7.19 | 7.20 | 6.81 | 9.23 |
| via `0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb` (DE) | 5.00 | **7.42** | 7.44 | 10.24 |
| via `02c483938539bd7820f72e48ed6056bab68e221e1108d23965a2903221495e4af7` (SG) | 4.95 | 4.46 | 7.37 | 7.51 |
| via `02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd` (IN-Mumbai) | 2.46 | 4.30 | 5.97 | 5.36 |
| via `0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7` (AU-Sydney) | 0.06 | 0.04 | 0.00 (0/3) | 8.07 (0/3 hash) |

The paired reference for every mux verdict is `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`
(probed 8.99 MB/s, `paired-ref.txt`): one reference row of the same cell ran on :1081 immediately
before each mux row (`*.paired.tsv`, `*.paired-rows.tsv`).

Endpoint ceiling (`ceiling.tsv`, 3 concurrent clients × 50 MB, 2 trials): **uplink 11.29 MB/s**
(single 9.78, x1.15 over 3 direct clients), **downlink 11.48 MB/s** (single 8.46, x1.36 over
via-0371ab4b / via-0281a102 / via-02c48393).

## Verdict table

From the final `### verdict` block at 12:23:50Z; the AG2 tunnels-2 rows replace the first attempt's
invalid ones. `w/g` = wire/goodput.

| set | dir | MB | median | bar/ratio | ok/n | w/g | mode | verdict | note |
|---|---|---|---|---|---|---|---|---|---|
| mux-compose-T2xL2 | down | 10 | 0.42 | x0.077 | 5/5 | 1.24 | paired | FAIL | vs t2 1.374 l2 1.112 w/g 1.24 |
| mux-compose-T2xL2 | up | 10 | 4.32 | x0.602 | 3/3 | 1.00 | paired | FAIL | vs t2 0.771 l2 0.655 |
| mux-compose-T2xL2 | down | 50 | 1.59 | x0.205 | 5/5 | 1.08 | paired | FAIL | vs better of t2 1.052 l2 0.999; best 8.21 < 0.9 x ceiling 11.48 |
| mux-compose-T2xL2 | up | 50 | 8.55 | x0.828 | 3/3 | 1.00 | paired | FAIL | vs better of t2 1.017 l2 0.903, no ceiling row |
| mux-compose-T2xL2 | down | 100 | 7.52 | x0.931 | 5/5 | 1.04 | paired | FAIL | NOCOMP |
| mux-legs-2 | down | 10 | 5.41 | x1.112 | 5/5 | 1.02 | paired | PASS | - |
| mux-legs-2 | up | 10 | 4.77 | x0.655 | 3/3 | 1.00 | paired | FAIL | - |
| mux-legs-2 | down | 50 | 8.21 | x0.999 | 5/5 | 1.05 | paired | PASS | - |
| mux-legs-2 | up | 50 | 8.48 | x0.903 | 3/3 | 1.00 | paired | FAIL | - |
| mux-spread-3 | down | 50 | 5.62 | x0.708 | 5/5 | 1.03 | paired | FAIL | - |
| mux-spread-3 | up | 50 | 9.53 | x0.964 | 3/3 | 1.00 | paired | PASS | - |
| mux-standby-5 | down | 10 | 5.82 | 7.61 | 5/5 | 1.05 | bar | FAIL | ref-via-0371ab4b |
| mux-standby-5 | up | 10 | 5.83 | 7.42 | 5/5 | 1.00 | bar | FAIL | ref-via-0281a102 |
| mux-standby-5 | down | 50 | 6.61 | 8.87 | 5/5 | 1.01 | bar | FAIL | ref-via-0371ab4b |
| mux-standby-5 | up | 50 | 9.54 | 10.33 | 5/5 | 1.00 | bar | FAIL | ref-via-0371ab4b |
| mux-tunnels-2 | down | 10 | 4.97 | x1.374 | 5/5 | 1.00 | paired | PASS | - |
| mux-tunnels-2 | up | 10 | 4.58 | x0.771 | 3/3 | 1.00 | paired | FAIL | - |
| mux-tunnels-2 | down | 50 | 7.95 | x1.052 | 5/5 | 1.01 | paired | PASS | - |
| mux-tunnels-2 | up | 50 | 10.14 | x1.017 | 3/3 | 1.00 | paired | PASS | - |
| mux-tunnels-2-up2 | up | 50 | 5.57 | 10.33 | 10/10 | - | bar | FAIL | ref-via-0371ab4b |

`mux-tunnels-2-up2`, two concurrent uploads: sum 11.33 vs bar 10.73 = min(ref sum 19.40,
0.95 x uplink ceiling 11.29) — **bound by the ceiling, PASS**; median ratio x0.593 over 5 trials,
5 hash-clean (`mux-tunnels-2-up2.up2.tsv`).

`mux-standby-5` ran in **bar mode**: its bars are the reference medians measured in the 10:54Z suite,
roughly an hour earlier, not interleaved rows — those four FAILs are not paired ratios.

Exit gate (`exit-resources.tsv`): every set PASS (`EXITRES … verdict=PASS`); the campaign-long slope
is `-182 KiB/min` over 41.5 min, n=19, **PASS**. Hashes: 53/53 on tunnels-2 + legs-2 + spread-3,
21/21 compose, 20/20 standby, 10/10 up2.

## Criterion status under goal text v4

1. **references — MET.** Full suite of 8 reference sets at 10:54–11:38Z with hop list, transport type
   and TpID per hop (`ref-*.tsv`, `*.target.json`); paired rows interleaved in every mux set except
   standby-5.
2. **stream-level (tunnels-2) — NOT MET, one cell short.** 10 down x1.374 PASS, 50 down x1.052 PASS,
   50 up single x1.017 PASS, two concurrent uploads PASS at the ceiling (sum 11.33 / bar 10.73);
   **10 MB single upload x0.771 FAIL** — trials 0.771 / 0.853 / **0.079** (row 8, 0.56 vs 7.11 MB/s).
3. **packet-level (legs-2) — downloads MET, uploads not.** 10 down x1.112, 50 down x0.999 (5 % band),
   rg port 49216 constant, 16/16 hashes; 10 up x0.655, 50 up x0.903.
4. **composition — NOT MET.** 10 down x0.077, 10 up x0.602, 50 down x0.205, 50 up x0.828, 100 down
   x0.931. See "what went wrong" (a).
5. **direction — PARTLY MET.** Forward on the direct route: tunnels-2 2 PASS / 0 FAIL, spread-3
   2/0, standby-5 3/0; legs-2 16/16 FAIL (its group holds two multihop legs and no direct leg, so
   forward sits on `fdab37dd`); compose 0 PASS / 1 FAIL / 20 INFO. Reverse fanout within the leg
   count: 29 PASS / 1 FAIL across the sets (`direction.tsv`, `*.direction.tsv`).
6. **degradation — NOT SCORED.** The cut row fired and the stream survived (5/5 hashes after the cut,
   no group lost or gained, one `tunnel_promoted`), but ttfb could not be measured. See (b).
7. **wire/goodput ≤ 1.2 — MET except one cell.** Every cell ≤ 1.08 except compose 10 MB down at 1.24.
   Churn events all carry named reasons (`*.mux_events.json`).
8. **standby-pool default — MET structurally.** standby-5 discovered the pool on its own (2 → 3 → 5
   groups in 10 s, quiet 20 s), `pool_size 5 > 2` PASS, `survivors_kept 6/6`, `rg_ports_lost none`,
   `rg_ports_gained none`, `promote_event tunnel_promoted x1`, both reorder-wedge counters 0.
9. **no-pins run — RAN, below the bars.** standby-5 is that run: 10 down 5.82 / 7.61, 10 up
   5.83 / 7.42, 50 down 6.61 / 8.87, 50 up 9.54 / 10.33 (bar mode, see above).
10. **spread — NOT MET.** `routes_active` min 3 max 3 PASS, `max_share 0.4590 ≤ 0.484` PASS (0.4 plus
    one 4 MiB chunk of slack), `ratio_50up 0.964` PASS, **`ratio_50down 0.708` FAIL**, 8/8 hashes,
    knobs landed live in 6 s (`mux-spread-3.assert.tsv`, `mux-spread-3.settings.json`).

## What went wrong

**(a) The composition cell collapsed because the slow AU route was pinned into group 2.**
`run-compose.sh` took its pins in `ls` order, so `mux-compose-T2xL2.rg49220.target.json` pairs
`0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7` (AU-Sydney, 470 ms) with
`0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb`, while group 1
(`rg49219`) got the two fast routes `03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf`
and `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`. That AU route's own
reference in this campaign was 0.06 MB/s at 10 MB down and 0/3 complete at 50 MB. The exit's
reverse-direction scheduler kept feeding it a share anyway: in `mux-compose-T2xL2.carrier.tsv` the
`50cdb857` `recv_delta` grows 0.49 → 1.24 → 1.15 → 2.15 → 1.94 MB across the five 10 MB trials, and
each trial's duration tracks those bytes at roughly 60 KB/s — 8.3 s, 15.0 s, 24.0 s, 31.9 s, 37.4 s
(`mux-compose-T2xL2.tsv`), i.e. the whole transfer waits on the AU leg's share. Both halves are fixed
forward: **#5038** (merged `c537336368c16a4426f11820b9ec28773108a508`) ranks the default pin order by
paired rank instead of `ls` order, and **#5037** (open, being measured in chain AL) feeds a leg whose
delay basis is a multiple of its sibling's a probe rather than a proportional share.

**(b) The cut row fired too late to measure ttfb.** `mux-standby-5.chaos.tsv` records
`cut_at_s 5.180` with `bytes_before_cut 48955392` — 48.96 of the 50 MB were already in when the
transport was removed, so no byte arrived after the cut and `ttfb_after_cut_s` has nothing to report.
This is a bench cadence fault, not a survival failure: the row's hashes are 5/5, the surviving groups
were kept without a rebuild, and goodput went 9.45 → 9.08 MB/s. The chaos row now cuts on time
(**#5038**); the row should be read as "late", not as a criterion 6 failure.
