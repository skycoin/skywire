# 67dddb8be — the paired-reference baseline of 2026-09-17

Commit under test: 67dddb8be "bench,docs: the standby pool's before/after smokes,
the failover before-row, and the chunked-upload plan (#4989)" — the pre-merge tip of
#4989, on top of #4985/#4986/#4987/#4988.
Ran: 2026-09-17 18:12Z-18:50Z, chain21 (scratchpad/chain21.log; compose leg re-run
from 284eaf707 recorded in compose21.log).
Sets: pick-ref + ref-direct-{stcpr,squicr} + ref-via-{0255117b,0281a102,02a2d4c3,
02c48393,0371ab4b,03f57e7c}, mux-legs-2, mux-tunnels-2, mux-tunnels-2-up2,
mux-compose-T2xL2 (INVALID), mux-standby-0 (INVALID).
## Verdict rows (paired vs 0371ab4b)
    mux-legs-2     down 10  4.80 MB/s  x1.110 PASS   up 10  6.90  x0.978 PASS
    mux-legs-2     down 50  5.46       x0.991 PASS   up 50  8.61  x0.841 FAIL
    mux-tunnels-2  down 10  4.97       x1.276 PASS   up 10  4.82  x0.835 FAIL
    mux-tunnels-2  down 50  2.62       x0.848 FAIL   up 50  0.50  x0.055 FAIL
    mux-tunnels-2-up2 up 50  sum 9.21/9.19 vs ref_sum 19.09/18.97 — ratio 0.482/0.484

The 50 MB upload collapse (0.50 MB/s, wire/good 0.01, carrier 0/3) is the defect that
the rest of the day chased.
## Cut row (inside mux-tunnels-2)
Cut tp 95839ad0-b588-0b1d-8475-45fba6ae993c at 3.257 s after 4194304 bytes; 16 rows,
hash_ok=16. mux-standby-0 INVALID: the pool did not grow past the active set
(0 groups with --tunnels 2), so there is no standby failover row here.

## Exit gate
mux-tunnels-2 FAIL (rss +66 MiB > 64 MiB); mux-legs-2 PASS; mux-tunnels-2-up2 PASS;
mux-compose-T2xL2 SKIP (missing pre row).

Outcome: the branch shipped as f453df391 (#4989). This run is kept as the reference
baseline the later chains' bars and paired ratios are read against.
