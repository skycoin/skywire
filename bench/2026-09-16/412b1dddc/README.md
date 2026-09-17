# 412b1dddc — mux suite after #4962 (deadlock fix, no lone-leg parks, 100 ms window refresh), 1 MiB range chunks

References: `../98ff6be85/`. Before rows: `../80f6c5a6c/`. Sets ran through the default
`skysocks-client` on :1080 with `--range-port 18080 --range-chunk-kib 1024`; legs sets pin `mux width N`.

Verdicts (`bench/verdict.sh ../98ff6be85 .`), median MB/s against the best single-route reference:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 3.37 | 6.49 | 4.43 | 9.91 |
| tunnels-2 | 2.00 | 1.64 | 3.25 | 8.79 |
| tunnels-3 | 2.14 | 4.50 | 3.26 | 8.81 |
| legs-2 | **4.27** | 4.38 | **5.87** | 8.29 |
| legs-3 | **3.56** | 3.75 | **6.81** | 8.36 |

What the rows say:

- No session froze: legs-3 completed 20/20 where campaign10's deadlocked at 34 MB (#4962).
- Writer parks are zero at both ends in every set; wire/goodput ≤ 1.16 everywhere.
- 1 MiB chunks balance the 10 MB download across tunnels (0.92 → 2.00) but cost the 50 MB one
  (4.47 → 3.25): one exit connect plus one request round trip per chunk.
- Tunnel uploads are bimodal (1.1–2.0 vs 9.6 MB/s): a single POST lands on whichever tunnel has
  the fewest streams, blind to its speed → #4963 (capacity-weighted pick).
- With every upload on the direct tunnel (tunnels-3), 10 MB ran at 4.5 MB/s against the 6.49
  non-mux reference on the SAME route, and 50 MB at 8.8 vs 9.1: a near-constant ~0.6 s per transfer.
  The references ran without mux; `--tunnels > 1` implies mux. To be measured next.
- legs-2: the exit never activated our second leg (its snapshot: `dc5330cf standby`), so its
  downloads rode one leg and still beat the 03f57e7c reference (5.9–7.7 vs 3.0) — that reference
  is 12 h old; campaign12 re-measures the references on the same binary.
- legs-3: the exit parked both aux legs 45 s in ("data progress stalled with an open reorder gap"),
  then the stripe set flapped (its journal: our leg-state resync marks leg 2 standby and active in
  the same millisecond); the exit stormed on the 0371ab4b leg (3010 retransmits, ack delay 971 ms).
  Uploads striped a third per leg at 8.4 MB/s; our side 505 retransmits, 2 reorder wedges.
