# 1ce356b2d — mux suite after #4966 (one-round-trip range chunks, early start), #4967 (--rg by tunnel port), #4968 (30 s park hold), #4969 (idle leg is not stalled; peer parks are events); DEFAULT 4 MiB range chunks

Mux sets only; the bar is the reference run from `../a8c3b6486/` (same day, same rig, same
`skysocks-client` on :1080 with `--range-port 18080`, now at the default chunk size — campaign13 used
1 MiB). Legs sets pin `mux width N`; tunnels sets use N sibling route groups. Exit =
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`, also on 1ce356b2d.

Verdicts (`bench/verdict.sh bench/2026-09-16/a8c3b6486 bench/2026-09-16/1ce356b2d`), median MB/s
against the best single-route reference of the a8c3b6486 run; campaign13 (52e430fff) in parentheses:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 5.51 (via 03f57e7c) | 6.81 (direct stcpr) | 8.05 (via 0371ab4b) | 9.95 (via 0371ab4b) |
| legs-2 | 5.40 (5.71) | 3.28 (3.73) | 7.25 (7.63) | 8.98 (8.62) |
| legs-3 | 3.20 (3.39) | 2.12 (3.91) | 6.85 (7.51) | 8.28 (7.56) |
| tunnels-2 | 3.25 (1.94) | 6.00 (1.32) | **8.32** (3.78) | 9.82 (8.90) |
| tunnels-3 | 2.65 (2.19) | 4.62 (6.03) | 7.81 (3.45) | 8.90 (8.86) |

- **Tunnels-2 passes a cell for the first time: 50 MB down 8.32 vs the 8.05 bar** (was 3.78), at
  wire/goodput 1.01 and 5/5 hashes. 10 MB down is 3.25 (was 1.94) and the uploads are 6.00 / 9.82.
  The split is by capacity, roughly 1/3 on the Sydney tunnel (50cdb857 via 0255117b) and 2/3 direct
  (b414796d). #4966 is confirmed live; 10 MB is only three 4 MiB chunks, so there is little left to
  parallelise at that size and the cell stays under the bar.
- **Tunnels-3 lost two 10 MB download trials to an all-paths blackout, not to the splitter.**
  Trials 4 and 5 returned http 000 after exactly 15.0 s — 3/5 hashes, the only non-5/5 cell in the
  suite. A ~55 s blackout (04:21:20–04:22:18Z) froze all three route groups at once: our reorder
  sequence is frozen across the row snapshots, exit ack delay ran 28.7 s and 39.3 s with idle-PTO TLP
  probes, and traffic resumed on a `unidir-flip: FLIPPED` log line. The splitter was never entered;
  the 15.0 s is the client's exit-open sniff timeout, which closes the browser connection silently.
  Same signature as the plain-route stall in `../a8c3b6486/ref-via-0371ab4b` row 13, so this is a
  path-level event the mux inherits. Mitigation PR in flight: log it, count it, and let the tunnel
  sit out the next picks. 50 MB down is 7.81 vs the 8.05 bar.
- **legs-2: #4969 works — the exit now uses both legs for 50 MB downloads.** The aux leg carries
  9.5–36 MB per row where campaign13 had it at 0, and peer-mirrored park/promote now surface as
  events (`by=peer`) instead of being adopted silently. The whole set has exactly one held
  `shared bottleneck` park, and that one is correct: uploads share this host's ~10 MB/s uplink, so
  there really is one pipe.
- **Two legs still do not sum.** legs-2 10 MB down is 5.40 vs 5.51 and 50 MB down 7.25 at
  wire/goodput 1.11. Root cause, from the exit journal and counters: the earliest-completion picker
  and the RACK loss threshold both use FIRST-HOP RTT (exit→Amsterdam ~10 ms vs exit→Atlanta ~95 ms)
  while the end-to-end feedback delay is ~170 ms on both legs. So the picker holds the slow leg back
  until ~9 frames have queued (row 11 splits 41.5 / 9.5 MB) and, once it spills, RACK at 1.25× of the
  short basis declares the in-flight frames lost: 199 retransmit bursts over 1824 packets, which is
  the ~27 MB of excess wire. Our receiver counters are clean (reorder pending 0, wedges 0) and the
  window sizing already uses the right basis — it is the picker and RACK that need the end-to-end
  number. Fix PR in flight.
- **legs-3 churns on a 30 s period now instead of 5 s.** #4968's hold works; what survives is the
  latency-band controller flipping a leg on single load-inflated RTT samples — 37 → 136 → 771 → 955
  → 435 → 346 ms events between 23:27 and 23:30 local. 10/50 MB down are 3.20 / 6.85 and the uploads
  2.12 / 8.28, all at wire/goodput ≤ 1.10. Fix in flight: band decisions off a windowed min-RTT
  rather than the latest sample.
- Criteria status: wire/goodput is ≤ 1.19 in every cell of the suite, so criterion 7 is met on that
  half; every park and promote now carries a named reason; hashes are 98/100, the two misses both
  from the tunnels-3 blackout.

Next: PRs for the ECF end-to-end RTT basis (picker + RACK) and the latency band's windowed min-RTT,
plus exit-open timeout visibility so a 15 s silent close is countable instead of an http 000.
campaign15 re-runs the mux sets on the merge of those; composition (`run-compose.sh`) and degradation
(`run-degrade.sh`) follow.

## Composition on this commit (`bench/run-compose.sh`, first run)

Per-tunnel leg shaping works since #4967: `proxy start --tunnels T`, then `mux set --rg <dst_port>
--legs … --prune` on each group gave T2xL2 = {03f57e7c+0371ab4b} × {0255117b+0281a102} and T3xL2
adding {02c48393+02a2d4c3}; dst_ports constant, 5/5 hashes in every cell.

| set | down 10 | down 50 | up 50 |
|---|---|---|---|
| T2xL2 | 3.86 (w/g 1.69) | 5.63 (w/g 1.47) | 8.53 |
| T3xL2 | 3.21 (w/g 1.23) | 7.13 (w/g 1.01) | 8.09 |

Worse than tunnels-2 alone (8.32): T2xL2 is an exit-side retransmit storm on the legs inside each
tunnel (4579 retransmits, 27 MB duplicate on one leg) — the first-hop-RTT defect #4970 fixes; T3xL2
avoids it only because the co-bottleneck parker collapses every tunnel to one leg from row 12
(44 events: 11 parks, 11 promotes). Re-measured on the #4970 build in the next campaign.
