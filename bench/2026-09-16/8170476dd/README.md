# 8170476dd — full suite after #4972 (send window on first-hop baseline + feedback delay; lone stream to the lowest-latency tunnel), #4973 (dmsg socks client), #4974 (chunk failover), #4975 (route groups close with the app); default 4 MiB chunks

All four mux sets plus composition, on the same frozen rig; the bar is the reference run from
`../a8c3b6486/` (same day, same rig, same `skysocks-client` on :1080 with `--range-port 18080`,
default 4 MiB chunks). Legs sets pin `mux width N`; tunnels sets use N sibling route groups. Exit =
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`, also on 8170476dd.

Drift probe before the sets (`drift.tsv`, 2 trials each, 50 MB): direct stcpr down 5.11 MB/s vs the
a8c3b6486 reference 4.52, **up 9.65 vs 6.16 = 1.57x**; via
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` down 8.98 vs 8.06, up 9.65 vs
9.95. So the direct path has drifted up markedly since the bars were taken and the Atlanta path is
roughly where it was — read the upload cells against the bar with that in mind. A fresh reference set
is being measured into this directory now; the final verdict for this commit will use it, and any
`ref-*.tsv` here belongs to that run, not to a8c3b6486.

Verdicts (`bench/verdict.sh bench/2026-09-16/a8c3b6486 bench/2026-09-16/8170476dd`), median MB/s;
**the bar row is a8c3b6486**. campaign16 (cfb558a32) in parentheses where that set was valid:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 5.51 (via 03f57e7c) | 6.81 (direct stcpr) | 8.05 (via 0371ab4b) | 9.95 (via 0371ab4b) |
| tunnels-2 | 3.30 (3.42) | 4.75 (6.53) | 7.82 (6.80) | 9.07 (8.85) |
| tunnels-3 | 2.79 (3.40) | 4.62 (4.55) | 7.17 (6.87) | 8.89 (8.80) |
| legs-2 | 4.16 | 4.69 | 7.51 | 8.67 |
| legs-3 | 3.83 | 3.65 | 6.60 | 7.53 |
| compose-T2xL2 | 3.59 | 3.00 | 7.15 | 8.59 |

cfb558a32's legs-2 set was marked `.INVALID` (it ran on an unpinned group left over from the tunnels
set) so there is no comparison cell, and that campaign ran no legs-3 or compose set.

## Cross-campaign 50 MB medians (the plateau)

Download, MB/s, bar 8.05:

| set | 1ce356b2d | 4cc8e9b6b | cfb558a32 | 8170476dd |
|---|---|---|---|---|
| tunnels-2 | 8.32 | 7.25 | 6.80 | 7.82 |
| tunnels-3 | 7.81 | 2.95 | 6.87 | 7.17 |
| legs-2 | 7.25 | 8.46 | INVALID | 7.51 |
| legs-3 | 6.85 | 5.31 | – | 6.60 |
| compose-T2xL2 | 5.63 | 6.67 | – | 7.15 |

Upload, MB/s, bar 9.95:

| set | 1ce356b2d | 4cc8e9b6b | cfb558a32 | 8170476dd |
|---|---|---|---|---|
| tunnels-2 | 9.82 | 2.80 | 8.85 | 9.07 |
| tunnels-3 | 8.90 | 8.83 | 8.80 | 8.89 |
| legs-2 | 8.98 | 8.36 | INVALID | 8.67 |
| legs-3 | 8.28 | 7.20 | – | 7.53 |
| compose-T2xL2 | 8.53 | 8.09 | – | 8.59 |

Four campaigns of control-plane fixes move individual cells by 1–5 MB/s when something was broken
(the 4cc8e9b6b ratchet, the cfb558a32 window), but nothing has moved the best cell past ~8.5.

- **Clean run, all criteria met.** All 100 rows are 5/5 on hashes in every cell, wire/goodput is
  ≤ 1.19 everywhere (worst: compose 10 MB down 1.19, legs-3 10 MB down 1.15; every 50 MB cell is
  1.00–1.10), and each group's dst_port is constant for the whole set — `49159`/`49160` tunnels-2,
  `49162`–`49164` tunnels-3, `49167` legs-2, `49169` legs-3, `49171`/`49172` compose. Churn, all with
  named reasons: legs-2 5 events (one `shared bottleneck: co-bottlenecked with a kept active leg`,
  one peer-mirrored park and its peer-mirrored promote, plus the operator `mux remove` and its
  `primary_rehomed`); legs-3 24 events (12 parks — 8 shared-bottleneck, 2 peer-mirrored, 2
  `latency band` including one `min-RTT 1860 ms over 30s`, against 10 non-operator promotes, 7 of
  them `latency band … back within the active set's band` on the Sydney leg); compose 11 (5 parks —
  3 peer-mirrored, 2 shared-bottleneck — 3 peer-mirrored promotes, the operator remove/rehome, and
  one `dial_decision: diversify … first hop 50cdb857`). No wedges, no retransmit storms.
- **#4975 confirmed.** legs-2 ran immediately after tunnels-3 and pinned correctly:
  `mux-legs-2.target.json` is exactly `fdab37dd` (NL) + `95839ad0` (Atlanta) on a fresh port 49167,
  one `group_created`, one `leg_added: route setup: primary leg`. campaign16's legs-2 had to be
  thrown out because the previous set's group was still alive and the pin landed on it; with route
  groups now closing when the app stops, that failure mode is gone.
- **The result of this campaign is a plateau, and it is not leg- or tunnel-count-shaped.** Every
  variant's 50 MB download lands in 6.60–7.82 MB/s against an 8.05 bar, and every upload in
  7.53–9.07 against 9.95 — two legs, three legs, two tunnels, three tunnels and the 2x2 composition
  all inside a ~1.2 MB/s band, with clean counters and no visible stall. Mux is no longer losing to
  a bug; it is simply not beating one good route. **The next experiment is to run two independent
  plain routes downloading concurrently** (no mux, two separate proxy instances) and add their
  rates. If the pair also totals ~8–9 MB/s, the host or the exit path is the cap, the bar *is* the
  ceiling, and "≥ best reference" is already met within noise — the campaign's remaining work is
  degradation and reconnect, not throughput. If the pair sums to noticeably more, the sender is
  leaving capacity unused and the next target is the scheduler/window, not the transport.
- **10 MB downloads stay 25–50% under the bar in every variant** (2.79–4.16 vs 5.51) while the 50 MB
  cells are near it. That is the range-split startup, not the mux: 10 MB is three 4 MiB chunks, so
  there is almost no parallelism to win, and the first chunk is largely drained before the split
  starts paying. The 10 MB cells also carry the only wire/goodput above 1.10. Worth testing a
  smaller first chunk (or a chunk size that scales with the body) before reading these cells as a
  mux result at all.

Next: finish the in-flight reference set in this directory and re-run `verdict.sh` against it for the
final numbers; then the two-concurrent-plain-routes ceiling test above, since it decides whether the
remaining gap is even ours. After that, degradation (`run-degrade.sh`) on the #4974/#4975 behaviour —
a tunnel's group closing should now fail its chunk fast and refetch on a surviving tunnel, which was
the 35–40 s resume in campaign15 — a dmsg-only reference set (#4973 makes the standalone socks client
carry over dmsg), and the standby-pool phase. Still open from the campaign: silent stalls and
reconnect after a mid-set death.

## Fresh references and the capacity check (same commit, same hour)

`ref-*.tsv` in this directory were measured right after the mux sets (`bench/verdict.sh . .`):
bars became 10 MB down 6.17 (via 03f57e7c), 10 MB up 5.97 (via 0281a102), 50 MB down 8.84 (via
03f57e7c), 50 MB up 9.99 (via 0371ab4b); the Sydney route completed 16/20. Against these every mux
cell fails: the best 50 MB download is tunnels-2 at 7.82 (88 % of the bar), the best upload 9.07 (91 %).

`bench/run-capcheck.sh` (`capcheck.tsv`): two independent single-route clients (direct stcpr on :1080,
via 0371ab4b on :1081) transferring at the same instant.

| direction | A | B | sum | best alone | ratio |
|---|---|---|---|---|---|
| 50 MB down | 5.14 | 5.27 | **10.41** | 7.40 | 1.41 |
| 50 MB up | 5.30 | 8.64 | **13.95** | 9.75 | 1.43 |

The path has at least 40 % more capacity than any single route uses, and neither mux variant reaches
it: two legs in one group deliver 7.5, two tunnels 7.8. The bound is in the sender, not the network;
the earlier belief that this host's uplink caps at 10 MB/s was wrong. Investigation of what bounds one
route group (exit CPU per group, window arithmetic, receiver head-of-line) follows.
