# 52e430fff — mux suite after #4964 (exit-side parking) + #4965 (tunnel meter), 1 MiB range chunks

Mux sets only; the bar is the reference run from `../a8c3b6486/` (same day, same rig, same
`skysocks-client` on :1080 with `--range-port 18080 --range-chunk-kib 1024`). Legs sets pin
`mux width N`; tunnels sets use N sibling route groups. Exit = `022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`,
also on 52e430fff.

Verdicts (`bench/verdict.sh bench/2026-09-16/a8c3b6486 bench/2026-09-16/52e430fff`), median MB/s
against the best single-route reference of the a8c3b6486 run:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 5.51 (via 03f57e7c) | 6.81 (direct stcpr) | 8.05 (via 0371ab4b) | 9.95 (via 0371ab4b) |
| legs-2 | **5.71** | 3.73 | 7.63 | 8.62 |
| legs-3 | 3.39 | 3.91 | 7.51 | 7.56 |
| tunnels-2 | 1.94 | 1.32 | 3.78 | 8.90 |
| tunnels-3 | 2.19 | 6.03 | 3.45 | 8.86 |

One cell passes: legs-2 10 MB down, 5.71 vs the 5.51 bar, 5/5 hashes, wire/goodput 1.03, both legs
carrying every trial (fdab37dd 7.0–9.3 MB, 95839ad0 2.0–3.3 MB) and no park on either side. Every
other cell is under the bar; hashes are 80/80 and no session died.

- **Tunnels: the split is back.** #4965's busy-only meter fixed the a8c3b6486 starvation where a
  sibling tunnel carried 0–69 bytes. tunnels-2 downloads now split 2.2/8.0, 4.2/6.0, 4.3/5.9,
  4.8/5.3, 4.9/5.6 MB across 50cdb857 and b414796d; tunnels-3 splits three ways
  (1.1/4.9/4.3 … 1.2/7.9/1.3).
- **The picker steers uploads to the direct tunnel in tunnels-3.** All five 10 MB uploads went
  entirely over b414796d (direct) — 6.03 MB/s median, the best mux upload cell in the suite —
  while tunnels-2 sent four of five uploads over the via-tunnel 50cdb857 and managed 1.32 MB/s.
  Same code, different candidate set: the capacity meter picks correctly once a direct tunnel is in
  the pool.
- **Tunnel downloads are still 2–3x under a plain route** (1.94 / 2.19 vs 5.51; 3.78 / 3.45 vs
  8.05) at wire/goodput 1.00–1.05, i.e. no retransmit tax — the loss is the per-chunk cost of the
  range-split itself (a serialized 1 MiB chunk request per tunnel per round). Fix in flight in PR
  `fix/rangesplit-pipelined-chunks`.
- **legs-2's 50 MB downloads ran on one leg, and the exit is what parked the other.** Exit journal
  03:43:05.689Z, :10.689Z, :15.689Z (= 22:43 local): `leg-dataprogress: parking 1 stalled leg(s) to
  warm standby (manual mode — pinned set kept for recovery; reorder gap age 0s, stuck=false)`. That
  fires during upload rows 8–10, where our own send picker had left 95839ad0 idle (it sent 9 537,
  17 and 16 bytes in those rows) — #4964 judges a leg by payload, and an idle leg reads as stalled.
  The park was mirrored to us over CapLegState and adopted silently by `handleLegStatePacket`
  (`route_group.go:4289`, no mux event — legs-2 recorded 11 events, none of them a park), after
  which our 7 s `legstate-resync` (`route_group.go:2396`) re-asserted standby=true for that leg on
  every tick: the exit logged `LegState: peer marked leg 1 standby` at :21, :28, :35 … through
  03:44:59, spanning all five 50 MB download rows. Result: rows 11–15 moved 50.0–50.4 MB on
  fdab37dd and exactly 1 byte on 95839ad0, at 7.63 MB/s median — one leg, 1.00 wire/goodput.
- **legs-3 flaps one leg every 5 s.** 28 mux events, 14 of them `leg_parked` on leg 1 (95839ad0):
  one `latency band: 49 ms is out of the active set's band` at 22:45:34, two `data progress stalled
  with an open reorder gap` at 22:46:44, then `shared bottleneck: co-bottlenecked with a kept active
  leg (one pipe, not two)` every 5 s from 22:47:14 to 22:48:19. The loop is inside a single
  data-progress tick: `legDataProgressServiceFn` calls `enforceBottleneckGroups`
  (`route_group.go:2588`) which parks the leg at `route_group.go:2987-2994`, then
  `enforceLatencyBand` (`route_group.go:2606`) re-admits it microseconds later at
  `route_group.go:3097-3101` because 49 ms is well inside the band — silently, with no event, which
  is why only the park half shows in the log. `pickBottleneckDemotions` skips standby legs
  (`bottleneck.go:312`), so a park that stuck would have ended the loop; the promote is what keeps
  it going. The exit sees both halves: `LegState: peer marked leg 1 standby` immediately followed by
  `… active` at 03:47:14, :19, :24, :29, :39, :45, :49, :55, :59, 03:48:04, :09, :14, :19 —
  13 flaps in 65 s, plus the 7 s resync re-asserting active in between. The uplink caps at ~10 MB/s
  here, so the shared-bottleneck verdict is correct during uploads; the park just does not hold.
- **The flap's cost is retransmits.** The 22:47:14–22:48:19 window covers exactly the 50 MB upload
  rows 16–20. Per-row recovery deltas against per-row wire/goodput: row 16 43 retx / 1.018,
  row 17 446 retx / 1.159, row 18 311 retx / 1.117, row 19 47 retx / 1.032, row 20 249 retx /
  1.103. At ~16.3 KB per sequence that accounts for 0.70, 7.28, 5.07, 0.77 and 4.06 MB against
  measured excesses of 0.90, 7.93, 5.85, 1.61 and 5.17 MB — 85–92% of every excess byte is a
  retransmit, and each park mirrors to the peer and triggers the demote-time flush of the in-flight
  window (`route_group.go:2020`). legs-3's 50 MB downloads show the same shape from the other side:
  row 12 wedged (`reorder_wedge` at 22:46:58, frontier stuck at seq=7318 for 1.867 s, pending=802)
  and cost 72.0 MB of wire for 50 MB of payload, 1.44. legs-3 10 MB down is 1.34 overall
  (per row 1.20, 1.63, 1.56, 1.34, 1.11) with ~0 local retransmits — those rows precede the flap
  and pay the 3-leg striping cost of a 470 ms leg (50cdb857 took 1.5–3.1 MB of every download)
  beside two ~50 ms legs. legs-2, with no flap and no third band, sat at 1.00–1.03 everywhere.
- Churn summary: legs-2 11 mux events, 0 parks; legs-3 28 events, 14 parks, 0 promotions recorded
  (the adaptive promote path emits none).

Next: PR `fix/router-park-hysteresis` (skycoin/skywire#4968) gives an adaptive park a 30 s minimum
hold and routes every adaptive promotion through one seam that refuses a held leg and emits the
event, so the 5 s loop cannot form; an operator pin still clears the hold. Two open items it does
not address — the exit parking an idle-but-healthy leg during an upload block and never re-admitting
it for the download block that follows (legs-2 rows 11–15), and `handleLegStatePacket` adopting a
peer's park with no local event — plus `fix/rangesplit-pipelined-chunks` for the tunnel download
cells. Re-run the suite on the merge of all three.

## Flag experiments on this commit (`../52e430fff-exp/`, n=3, two tunnels)

| run | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| campaign13 baseline (1 MiB chunks, concurrency 8) | 1.94 | 1.32 | 3.78 | 8.90 |
| chunk4096 (the default) | 2.01 | 5.55 | 5.32 | 9.23 |
| conc2 (1 MiB, concurrency 2) | 1.38 | 4.52 | 1.24 | 9.01 |
| nosplit (no range split; downloads ride the direct tunnel) | 4.50 | 5.11 | 6.78 | 8.79 |
| tunnels1 + split (1 MiB) | 3.49 | 6.77 | 4.07 | 9.80 |

The splitter is the cost, not the tunnel: eight concurrent chunk streams inside one session
aggregate to less than one plain stream (6.78 → 4.07 on a single tunnel), and throughput tracks
bytes in flight (2 MiB 1.24, 8 MiB 3.78, 32 MiB 5.32). The slow tunnel's 13–42 % share is then a
straggler. Per-chunk setup round trips are secondary (#4966 removes two of three anyway). The next
campaign runs the default 4 MiB chunks; a visor profile during a split download is in progress.
