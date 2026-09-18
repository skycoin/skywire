# 0251e5da4-smoke — int-K2: int-K + #5012 as a goodput trial (chain AE)

Commit under test: 0251e5da4, int-K2 = int-K + PR #5012 amended so a shared-bottleneck park
is a **goodput trial** rather than a verdict. Ceiling-aware verdicts (`ceiling.tsv` from
chain Z).
Ran: 2026-09-18 05:58Z–06:20Z, chain AE (`scratchpad/chainAE.log`).
Sets: mux-tunnels-2, mux-legs-2 (5 down / 3 up), mux-compose-T2xL2 (2x2, 10/50 MB down+up,
100 MB down), plus `direction.sh`. Paired vs 0371ab4b.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  10   3.80    x0.673   5/5    1.06  paired  FAIL    vs t2 1.035 l2 0.678
mux-compose-T2xL2  up    10   4.80    x0.810   2/3    1.00  paired  FAIL    vs t2 0.998 l2 1.209 hash 2/3
mux-compose-T2xL2  down  50   7.13    x1.093   5/5    1.02  paired  FAIL    best 9.19 saturates ceiling 9.55 — bar 0.95 x ceiling = 9.07
mux-compose-T2xL2  up    50   3.90    x0.400   2/3    1.09  paired  FAIL    vs better of t2 1.015 l2 0.879, no ceiling row hash 2/3
mux-compose-T2xL2  down  100  8.33    x0.986   5/5    1.12  paired  PASS    NOCOMP
mux-legs-2         down  10   3.91    x0.678   5/5    1.00  paired  FAIL    -
mux-legs-2         up    10   4.60    x1.209   3/3    1.00  paired  PASS    -
mux-legs-2         down  50   6.73    x0.812   5/5    1.00  paired  FAIL    -
mux-legs-2         up    50   8.89    x0.879   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  10   5.61    x1.035   5/5    1.00  paired  PASS    -
mux-tunnels-2      up    10   6.48    x0.998   3/3    1.00  paired  PASS    -
mux-tunnels-2      down  50   9.19    x1.090   5/5    1.00  paired  PASS    -
mux-tunnels-2      up    50   10.25   x1.015   3/3    1.00  paired  PASS    -
```
All four tunnels-2 cells PASS. The composition set lost two upload hashes (19/21 rows
hash_ok) — the short-chunk 400 the sink returns when the client abandons a chunk, which
#5027 addresses. EXITRES PASS on all three sets.

Decided: the 100 MB composition cell finally clears the ceiling-aware bar (x0.986), and
park-as-a-trial keeps legs-2 at x0.812 instead of x0.862 — still short of the x1.13 that
parking-off reached in chain AD, so #5012 went back for the rework.
