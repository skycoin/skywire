# 6a86be622 — campaign19, after #4979 (first-stream handshake pipelined), #4981 (sibling tunnel on the best-ranked unused route, two tunnels by default) and #4982 (sink hashes an object once, optimistic CONNECT)

All four mux sets plus both compositions on the frozen rig, both ends at 6a86be622, exit
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`. The bars are a **fresh
reference set taken into this same directory** (`ref-*.tsv`) a few hours after the mux sets, same
`skysocks-client` on :1080 with `--range-port 18080`, default 4 MiB chunks — so the verdict is
`bench/verdict.sh bench/2026-09-16/6a86be622 bench/2026-09-16/6a86be622`.

## Why the bars were re-measured

The drift probe (`drift.tsv`, 2 trials, 50 MB, compared against the `../a8c3b6486/` references it
names in its header) came back split: via
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` down **0.62 and 9.33 MB/s
(mean 4.97) against that route's 8.05 reference**, while direct stcpr up read **8.62 / 10.59 (9.60)
against 6.16**. The uplink was healthy and better than reference; the Atlanta downlink was not. The
fresh reference set confirms it: `03f57e7c…` 50 MB down **6.62** (8.84 in the `../8170476dd/` set)
and `0371ab4b…` **6.09** (8.24). Both of the routes that made the 2x1 composition win in campaign18
degraded during this window, which is why the 50 MB download bar here is Singapore.

## Contemporary bars (fresh `ref-*` medians, MB/s)

10 MB down **6.18** via `03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf`;
10 MB up **6.37** direct stcpr; 50 MB down **6.63** via
`02c483938539bd7820f72e48ed6056bab68e221e1108d23965a2903221495e4af7` (Singapore);
50 MB up **9.96** direct stcpr.

## Verdicts

`bench/verdict.sh bench/2026-09-16/6a86be622 bench/2026-09-16/6a86be622`, median MB/s, wire/goodput
in parentheses where it is not 1.00–1.01:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (fresh ref) | 6.18 (via 03f57e7c) | 6.37 (direct) | 6.63 (via 02c48393) | 9.96 (direct) |
| compose-T2xL1 (best routes) | 5.12 | **6.72 PASS** | **8.70 PASS** | **10.33 PASS** |
| compose-T2xL2 | 4.44 (1.22) | 4.73 | 4.50 | 8.21 |
| legs-2 | 5.39 (1.11) | 4.74 | **6.63 PASS** (1.10) | 9.00 |
| legs-3 | 1.73 (1.20) | 4.85 | **7.34 PASS** (1.04) | 5.35 |
| tunnels-2 | 2.46 | 4.24 | 3.85 | 9.12 |
| tunnels-3 | 0.47 | 4.89 | 2.37 | 0.57 (4/5) |

**Five cells PASS** — the first passes any composition has taken on a download cell in the campaign.
`compose-T2xL1` (rg49184 = `stcpr>03f57e7c…@fdab37dd`, rg49185 = `stcpr>0371ab4b…@95839ad0`, one leg
each) takes three of its four: 10 MB up 6.72 vs 6.37, 50 MB down 8.70 vs 6.63, 50 MB up 10.33 vs
9.96. Only its 10 MB download fails, 5.12 vs 6.18. Its 50 MB downloads split across both tunnels
every row by `recv_delta` — 42/58, 34/66, 42/58, 50/50, 58/42 — at w/g 1.00, and its 50 MB uploads
are four rows at 10.33–10.38, above the direct bar.

`legs-2`'s 50 MB download lands exactly on the 6.63 bar (PASS) and `legs-3`'s at 7.34, so the pinned
two- and three-leg groups clear the degraded-route bar as well.

## Hashes and wire/goodput

**119 of 120 rows hash-verified.** The single miss is `mux-tunnels-3` row 17 (50 MB up, trial 2):
http 100, 34,527,744 of 50,000,000 bytes after 101.3 s — a stalled POST, not corruption. Wire/goodput
is 1.00–1.01 in every cell except four 10 MB/50 MB downloads on the multi-leg sets: compose-T2xL2
1.22, legs-3 1.20, legs-2 1.11 (10 MB) and 1.10 (50 MB), legs-3 1.04 (50 MB).

Churn, per `.mux_events.json`: compose-T2xL1 15 events (2 park / 2 promote), legs-2 14 (3/3, one
`reorder_wedge` at seq=681 cleared in 400 ms), legs-3 44 (11/12, seven wedges, all cleared in
0.9–6.4 s, parks named `latency band: min-RTT 1282 ms over 30s`, `… 5489 ms … is out of the active
set's band`, two `peer-mirrored park`), compose-T2xL2 27 (5/7, one wedge cleared in 2.4 s),
tunnels-2 5 (no parks), tunnels-3 23 (7 `group_created` against 4 `group_closed` and 5
`dial_decision` — the groups themselves churned). No wedge went uncleared and no session died.

## The auto-diversified tunnels sets chose badly — and why

#4981 ranks the sibling tunnel's candidate routes by **first-hop latency only**, over every transport
this visor holds. `mux-tunnels-2.mux_events.json`'s one `dial_decision` shows what that yields:

> `diversify: 1 sibling group(s) to 022716fb:3, excluding first-hop tp(s) b414796d …; ranked by
> first-hop latency: 0be8b6f0=1ms, 3e59e96d=28ms, 9fa91cce=52ms, f1012467=134ms, 5dfc8b3b=144ms,
> fdab37dd=144ms, … 50cdb857=200ms, … ac40965a=231ms, … 95839ad0=512ms, … ; chose 0be8b6f0;
> oracle: path over a free first hop; first hop 0be8b6f0`

`0be8b6f0` is `sudph>0326978f…`, a **LAN neighbour one millisecond away** whose second hop to the
exit was never measured — and the two routes that won the 2x1 set, `fdab37dd` (144 ms) and
`95839ad0` (512 ms), rank near the bottom of a 37-entry list precisely because their first hop *is*
most of the path. The result: tunnels-2 puts 8–25 % of every download on that tunnel
(`recv_delta` 4.2–12.7 MB of 50 MB) and its 50 MB download median falls to 3.85. tunnels-3 stacks two
more such picks (`dbdffeca`, then `93139b56`/`3e59e96d`/`14c258be` off a 250-entry ranking) and
collapses: 10 MB down 0.47, 50 MB down 2.37, 50 MB up 0.57 with rows of 255.8 s and 87.3 s beside
rows of 5.5 s.

This is a ranking bug, not a scheduler bug — the same scheduler drives compose-T2xL1's three passes.
The fix is **#4983: rank by first-hop *plus* second-hop path latency and refuse same-LAN first
hops**, under smoke now.

## Open after this run

The 10 MB download cell, still under the bar in every variant including the winning composition; the
stalled POST class (one in 120 rows here, 255.8 s and 87.3 s rows that did complete); and whether
#4983's ranking actually reproduces the pinned 2x1 shape automatically. Campaign Results live in
`docs/design/route-multiplexing-test-plan.md`.
