# a8c3b6486 — fresh references + mux suite after #4963 (capacity-weighted tunnel pick), 1 MiB range chunks

References and mux sets ran on the SAME commit and within the same two hours (the bars drifted
2x over the previous 12 h: direct-stcpr 10 MB down went 2.20 → 4.40, so `../98ff6be85/` is no
longer a valid bar). Sets ran through the default `skysocks-client` on :1080 with
`--range-port 18080 --range-chunk-kib 1024`; legs sets pin `mux width N`.

Verdicts (`bench/verdict.sh . .`), median MB/s against the best single-route reference of the same run:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 5.51 (via 03f57e7c) | 6.81 (direct stcpr) | 8.05 (via 0371ab4b) | 9.95 (via 0371ab4b) |
| tunnels-2 | 2.77 | 5.29 | 4.32 | 9.22 |
| tunnels-3 | 2.27 | 4.50 | 3.76 | 8.82 |
| legs-2 | 2.66 | 4.67 | 5.02 | 9.03 |
| legs-3 | 4.01 | 4.19 | 6.35 | 8.44 |

Every cell fails, and the carrier tables say why:

- **tunnels-2 and tunnels-3 rode one tunnel.** Every 10 MB download landed on b414796d; the other
  tunnels carried 0–69 bytes in all 5 trials (on 412b1dddc the same set split 4.2 / 5.9 MB). The
  #4963 meter recorded the rate of an idle window — keepalive bytes — as a tunnel's proven
  capacity; the primary's warm-up probes gave it ~1 KB/s, a ping gave the others ~23 B/s, and the
  ratio starved them before a chunk flowed. A starved tunnel is never busy, so its estimate was
  never revisited. Fixed in #4965 (busy-only sampling; a stale idle tunnel is probed while a
  transfer runs).
- **legs-2 and legs-3 lost their aux leg 22 s in.** `leg_parked` by adaptive, "data progress
  stalled with an open reorder gap": the receive-side stall detector judging legs by all bytes,
  the same defect #4964 fixes on the exit. After that park, legs-2 downloads ran on fdab37dd alone
  (trial 1 split 19.9 / 2.0 MB, trials 2–5 10 / 0). Trial 1's 2.2x wire/goodput is a real
  retransmit storm, not a snapshot artifact: one copy crossed the reorder frontier (~620 frames), but
  the exit kept striping onto the stalling aux leg and recovered every stripe over fdab37dd (exit
  counters for the set: 869 retransmits, 493 SACK-requested, 231 HoL, ack delay 530 ms; our SACKs
  sent 157 vs ~20 on a clean row). The other sets' first rows are 1.00–1.10x.
- Uploads on a single leg or tunnel pay the ~0.6 s mux startup (10 MB: 4.2–5.3 vs 6.81 non-mux;
  50 MB: 8.4–9.2 vs 9.95). Unchanged from 412b1dddc.
- wire/goodput ≤ 1.12 in every mux cell; no session died; hashes 80/80.
- Reference note: ref-via-0371ab4b 50 MB down trial 3 stalled silently for 15 s and delivered 0
  bytes (http 000) — the plain single-route stall, not a mux effect.

Next: `../52e430fff/` re-runs the mux sets with #4964 + #4965 against these references.
