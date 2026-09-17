# a5b611278 — the closing full suite (campaign18), after #4976 (bench), #4977 measured and closed, #4978 (a lone stream picks the tunnel with the lowest RTT per open stream); default 4 MiB chunks

The last full-suite run of the campaign: all four mux sets, the 2x2 composition and, new here, a
**2x1 composition on the two best-measured routes** (one leg per tunnel), all on the frozen rig with
both ends at a5b611278. The bar is the **fresh reference set in `../8170476dd/`** (`ref-*.tsv`,
measured on the same rig a few hours earlier, same `skysocks-client` on :1080 with
`--range-port 18080`, default 4 MiB chunks) — not the older `../a8c3b6486/` bars the earlier
campaigns used. Exit = `022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`,
also on a5b611278.

Drift probe before the sets (`drift.tsv`, 2 trials each, 50 MB): direct stcpr down 4.83 / 5.75 MB/s,
up 1.95 / 10.16; via `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` down
9.11 / 10.23, up 8.71 / 10.37. The Atlanta path reads at or above its 8.24 / 9.99 reference, so the
rig is in the state the references were taken in; the single 25.7 s direct-stcpr upload trial (1.95)
is the silent-stall signature described below, not drift.

Verdicts (`bench/verdict.sh bench/2026-09-16/8170476dd bench/2026-09-16/a5b611278`), median MB/s;
**the bar row is the fresh `8170476dd/ref-*` set**. Wire/goodput in parentheses where it is not 1.00–1.08;
hash misses called out:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 6.17 (via 03f57e7c) | 5.97 (via 0281a102) | 8.84 (via 03f57e7c) | 9.99 (via 0371ab4b) |
| compose-T2xL1 (best routes) | 4.48 | 4.45 | **8.36** | 8.71 |
| compose-T2xL2 | 4.50 | 3.17 (4/5) | 7.11 | 8.71 |
| legs-2 | 4.63 | 4.95 | 5.47 | 8.07 |
| legs-3 | 4.53 (1.25) | 4.80 | 6.76 | 8.05 |
| tunnels-2 | 3.63 | 4.65 | 6.99 | 5.84 (4/5) |
| tunnels-3 | 0.53 (1.27) | 4.64 | 6.51 | 8.90 |

Every cell FAILs the bar. The best download cell of the run is compose-T2xL1 at 8.36 = **95 % of the
8.84 bar**; the best upload 8.90 (tunnels-3) = 89 % of 9.99.

## Hashes and wire/goodput

**118 of 120 rows hash-verified.** The two misses are both stalls, not corruption:

- `mux-tunnels-2` row 17 (50 MB up, trial 2): the POST rode the Sydney tunnel
  (`50cdb857`, 40.5 MB of `sent_delta` against 11 KB on the direct `b414796d`), hit the 600 s cap
  with 43,843,584 of 50,000,000 bytes delivered, http 100. That one row is what pulls the
  tunnels-2 50 MB upload median down to 5.84 — the other four rows are 3.46 / 8.87 / 8.85 / 5.84.
- `mux-compose-T2xL2` row 7 (10 MB up, trial 2): http 000 after exactly 15.0 s, zero bytes — the
  client's exit-open sniff timeout, the same all-paths silent stall seen in every campaign since
  1ce356b2d and in the plain references too.

Wire/goodput is **1.00–1.08 in every cell except the two three-way 10 MB downloads**: tunnels-3
1.27 and legs-3 1.25. Every 50 MB cell in the suite is 1.00–1.06. Each group's dst_port is constant
for its whole set — `49160`/`49161` tunnels-2, `49167`–`49169` tunnels-3, `49173` legs-2, `49175`
legs-3, `49177`/`49178` compose-T2xL2, `49181`/`49182` compose-T2xL1 — and every carrier file's
trailer reads `before=… after=… constant`.

Churn, all with named reasons: tunnels-2 5 events, tunnels-3 8 (setup and two `dial_decision`
diversify lines), compose-T2xL1 14, legs-2 13 (one `shared bottleneck: co-bottlenecked with a kept
active leg (one pipe, not two)`, one `reorder_wedge` at seq=10915 cleared in 1.9 s), legs-3 28
(shared-bottleneck parks against `latency band … back within the active set's band` promotes, min-RTT
readings 135–1214 ms on the third leg), compose-T2xL2 35 (peer-mirrored park/promote pairs plus five
shared-bottleneck parks). No wedge went uncleared, no session died.

## What the sets say

- **The best-routes 2x1 composition is the winning shape.** `compose-T2xL1` pins
  rg49181 = `stcpr>03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf@fdab37dd`
  and rg49182 = `stcpr>0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13@95839ad0`
  — the two routes the reference set measured fastest — one leg each. Its 50 MB downloads split
  across both tunnels (34/66, 33/67, 50/50, 50/50, 58/42 by `recv_delta`) and land at 8.36 with
  w/g 1.00 and 5/5 hashes. Its uploads ride **one** tunnel per object, alternating between the two
  across rows — that is #4978's `rtt×(streams+1)` pick doing what criterion 5 asks of the forward
  direction.
- **Auto-diversified tunnels pick badly.** `tunnels-2` and `tunnels-3` take whatever first hop is
  free: `dial_decision … oracle: path over a free first hop; first hop 50cdb857` puts a tunnel on
  the 470 ms Sydney route, and tunnels-3 adds `d33d56d2`. Same scheduler, same code as the 2x1 set
  above, 6.99 and 6.51 on the 50 MB download against the 2x1 set's 8.36. tunnels-3's 10 MB download
  median of 0.53 is the extreme case: rows took 124.2, 68.2, 7.4, 18.8 and 2.6 s, hashes 5/5, with
  all three tunnels carrying bytes every row (row 1: 5.94 MB direct, 4.86 MB Sydney, 2.36 MB via
  d33d56d2 for a 10 MB object at w/g 1.27) — a 10 MB object is three chunks, so one straggler
  tunnel is the whole row.
- **Run-to-run variance is ±20 %, and it is not the scheduler.** legs-2's 50 MB download, on the
  same two pinned legs (`fdab37dd` + `95839ad0`) with the same code path, has measured **8.46 on
  4cc8e9b6b, 7.51 on 8170476dd and 5.47 here** — and within this run alone its five rows are 6.48,
  9.14, 21.0, 10.1 and 6.29 s (7.71, 5.47, 2.38, 4.95, 7.95 MB/s). No park, no wedge and w/g 1.06
  across all five. Any single cell of any campaign has to be read against that spread; it is why
  the campaign's "best ever" cells (tunnels-2 8.32 on 1ce356b2d, legs-2 8.46 on 4cc8e9b6b) never
  reproduced.
- **Composition still does not beat its components.** T2xL2 (two legs in each of two tunnels) is
  7.11 / 8.71 against T2xL1's 8.36 / 8.71 and legs-2's own best runs — adding the second leg inside
  each tunnel costs rather than adds, and its 35 events are the most churn in the suite.

## After-row for #4979 (`../bc0a4d35a/`)

#4979 pipelines the **first** stream's greeting and CONNECT the way a range chunk's already is,
removing one of the ~1.1 s prelude's three exit round trips. Re-measured on the same 2x1 best-routes
shape (rg49158 = `03f57e7c…@fdab37dd`, rg49159 = `0371ab4b…@95839ad0`), 3 trials, both ends at
bc0a4d35a:

| cell | a5b611278 (5 trials) | bc0a4d35a (3 trials) | bar |
|---|---|---|---|
| 10 MB down | 4.48 | 2.63 | 6.17 |
| 10 MB up | 4.45 | **6.82 PASS** | 5.97 |
| 50 MB down | 8.36 | 8.49 | 8.84 |
| 50 MB up | 8.71 | 8.95 | 9.99 |

- **10 MB up passes the bar** — 6.82 vs 5.97, 3/3 hashes, w/g 1.00, against 4.45 before.
- **50 MB down 8.49 = 96 % of the 8.84 bar**, the best download of any full run.
- **The 10 MB download median is the variance, not a regression.** Its three rows are 3.80 s, 1.89 s
  and 11.12 s. The 1.89 s row is the **fastest single 10 MB download of the whole campaign** — every
  a5b611278 row of the same cell was 2.06–2.33 s — which is #4979 doing exactly what it claims; the
  11.12 s row is one silent stall, and with n=3 it takes the median. All 3 hashes are correct.

Open after this run: the ~0.9 s of per-object prelude that survives #4979, the silent all-paths
stall (~1 per 100 transfers, present on plain routes too), 10 MB downloads 25–50 % under the bar in
every variant, and the route-ranking work — today's auto-diversify has no way to prefer the routes
that made the 2x1 set win. The campaign's Results section is in
`docs/design/route-multiplexing-test-plan.md`.
