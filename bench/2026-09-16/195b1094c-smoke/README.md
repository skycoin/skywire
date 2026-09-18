# 195b1094c-smoke — int-J: int-I + #5012 SBD-per-SACK (chain AB)

Commit under test: 195b1094c, int-J = int-I + PR #5012 (shared-bottleneck detection per
SACK). First ceiling-aware verdict run — `ceiling.tsv` copied in from chain Z, so criteria
2 and 4 are scored against uplink 11.23 / downlink 9.55 MB/s.
Ran: 2026-09-18 04:57Z–05:20Z, chain AB (`scratchpad/chainAB.log`).
Sets: mux-tunnels-2, mux-legs-2 (5 down / 3 up), mux-compose-T2xL2 (2x2, 10/50 MB down+up
plus 100 MB down), plus `direction.sh`. Paired vs 0371ab4b.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  10   3.09    x0.565   5/5    1.03  paired  FAIL    vs t2 0.988 l2 0.825
mux-compose-T2xL2  up    10   2.99    x0.614   3/3    1.00  paired  PASS    vs t2 0.623 l2 0.511
mux-compose-T2xL2  down  50   5.29    x0.631   5/5    1.05  paired  FAIL    best 8.90 saturates ceiling 9.55 — bar 0.95 x ceiling = 9.07
mux-compose-T2xL2  up    50   9.23    x0.896   3/3    1.00  paired  FAIL    vs better of t2 1.023 l2 0.817, no ceiling row
mux-compose-T2xL2  down  100  7.13    x0.858   5/5    1.02  paired  FAIL    NOCOMP
mux-legs-2         down  10   4.81    x0.825   5/5    1.00  paired  FAIL    -
mux-legs-2         up    10   3.67    x0.511   3/3    1.00  paired  FAIL    -
mux-legs-2         down  50   5.26    x0.777   5/5    1.00  paired  FAIL    -
mux-legs-2         up    50   8.41    x0.817   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  10   5.56    x0.988   5/5    1.01  paired  PASS    -
mux-tunnels-2      up    10   4.20    x0.623   3/3    1.01  paired  FAIL    -
mux-tunnels-2      down  50   8.90    x1.182   5/5    1.00  paired  PASS    -
mux-tunnels-2      up    50   9.90    x1.023   3/3    1.00  paired  PASS    -
```
Direction: legs-2 16 of 16 forward rows FAIL (`fwd fdab37dd other`). EXITRES PASS on all
three sets, but `EXITRES-SLOPE ... FAIL drift +107.0 MB over the run at 5.0 MB/min` on the
compose set.

Decided: tunnels-2 is clean (50 MB down x1.182, 50 MB up x1.023) while legs-2 sits at
x0.777 with #5012's parking active — which is the question chain AD's sweep was built to
answer.
