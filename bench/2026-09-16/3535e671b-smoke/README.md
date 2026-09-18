# 3535e671b-smoke — int-M: #5027 short-chunk retry + #5012 reworked

Binary `3535e671b` (int-M) = develop + #5027 (a striped upload survives a chunk the client abandoned — the sink's
short-chunk 400 is retried) + #5012 reworked (shared-bottleneck demotion off by default, per-leg sampling).
Chain AH, 2026-09-18 07:46Z–08:14Z (`scratchpad/chainAH.log`). Sets: mux-tunnels-2, mux-legs-2 (5/3 trials),
mux-compose-T2xL2 10/50 MB + 100 MB down; paired reference `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`,
ceiling-aware against `ceiling.tsv` (uplink 11.23, downlink 9.55 MB/s).
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  10   4.15    x0.748   5/5    1.29  paired  FAIL    vs t2 0.998 l2 0.723 w/g 1.29
mux-compose-T2xL2  up    10   0.40    x0.057   3/3    1.30  paired  FAIL    vs t2 0.955 l2 0.927 w/g 1.30
mux-compose-T2xL2  down  50   4.18    x0.770   4/5    1.11  paired  FAIL    best 9.09 saturates ceiling 9.55 — bar 0.95 x ceiling = 9.07 hash 4/5
mux-compose-T2xL2  up    50   1.21    x0.120   3/3    1.24  paired  FAIL    vs better of t2 0.930 l2 0.841, no ceiling row w/g 1.24
mux-compose-T2xL2  down  100  4.92    x0.613   5/5    1.41  paired  FAIL    NOCOMP w/g 1.41
mux-legs-2         down  10   4.04    x0.723   5/5    1.52  paired  FAIL    - w/g 1.52
mux-legs-2         up    10   6.69    x0.927   3/3    1.09  paired  FAIL    -
mux-legs-2         down  50   9.09    x1.108   5/5    1.03  paired  PASS    -
mux-legs-2         up    50   8.38    x0.841   3/3    1.11  paired  FAIL    -
mux-tunnels-2      down  10   5.31    x0.998   5/5    1.02  paired  PASS    -
mux-tunnels-2      up    10   6.34    x0.955   3/3    1.00  paired  PASS    -
mux-tunnels-2      down  50   8.35    x1.003   5/5    1.01  paired  PASS    -
mux-tunnels-2      up    50   9.43    x0.930   3/3    1.00  paired  FAIL    -
```
Hashes 16/16, 16/16, 20/21 — #5027's retry held the striped uploads chain AF lost 0/3 of, and legs-2 50 MB down cleared
the bar at x1.108 with demotion off. Composition is the regression: 10 MB up 0.40 (x0.057) and 50 MB up 1.21 (x0.120),
the worst upload cells of the day. Direction `mux-tunnels-2 fwd b414796d direct PASS=7 FAIL=1`, `mux-legs-2 fwd fdab37dd
other PASS=5 FAIL=6`; exit gate all three sets PASS (compose `d_rss=40120 KiB cpu=66.5s wall=673s`, `EXITRES-SLOPE 2011 KiB/min over 23.2 min PASS`).

Outcome: #5027 merged as `0db22347b`. #5012's rework did not merge from here — the composition upload collapse sent it
back and it landed from chain AI ([`e128db1ff-smoke`](../e128db1ff-smoke/)) as `da1709895`.
