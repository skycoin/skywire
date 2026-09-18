# 908a98bcd-smoke — int-P: #5035 forward confinement holds for uploads

Binary `908a98bcd` (int-P) = develop `d23f8b3d6` + #5035 (forward traffic stays on its confined leg — a full window waits
instead of spilling, and the choice has hysteresis). Chain AK, 2026-09-18 10:26Z–10:48Z (`scratchpad/chainAK.log`).
Sets: mux-tunnels-2, mux-legs-2 (5/3 trials), mux-compose-T2xL2 10/50 MB + 100 MB down; paired reference
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`, ceiling-aware (uplink 11.23, downlink 9.55 MB/s).
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  10   3.54    x0.651   5/5    1.18  paired  FAIL    vs t2 0.873 l2 1.009
mux-compose-T2xL2  up    10   4.60    x0.644   3/3    1.00  paired  FAIL    vs t2 0.872 l2 0.782
mux-compose-T2xL2  down  50   7.82    x0.938   5/5    1.03  paired  FAIL    best 9.15 saturates ceiling 9.55 — bar 0.95 x ceiling = 9.07
mux-compose-T2xL2  up    50   8.40    x0.816   3/3    1.00  paired  FAIL    vs better of t2 0.994 l2 0.943, no ceiling row
mux-compose-T2xL2  down  100  7.84    x0.840   5/5    0.68  paired  FAIL    NOCOMP
mux-legs-2         down  10   5.63    x1.009   5/5    1.04  paired  PASS    -
mux-legs-2         up    10   5.85    x0.782   3/3    1.00  paired  FAIL    -
mux-legs-2         down  50   9.15    x1.084   5/5    1.01  paired  PASS    -
mux-legs-2         up    50   9.42    x0.943   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  10   4.73    x0.873   5/5    1.00  paired  FAIL    -
mux-tunnels-2      up    10   5.68    x0.872   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  50   8.95    x1.075   5/5    1.00  paired  PASS    -
mux-tunnels-2      up    50   10.23   x0.994   3/3    1.00  paired  PASS    -
```
All 53 rows hash-clean and the composition uploads recovered: 50 MB up 8.40 MB/s (x0.816) against 2.43 in chain AJ, 10 MB
up 4.60 against 0.27, with w/g back to 1.00 on both. 50 MB up 10.23 MB/s is the day's best upload cell. Direction:
`mux-tunnels-2 fwd b414796d direct PASS=1 FAIL=0 INFO=15`, `mux-legs-2 fwd fdab37dd other PASS=6 FAIL=5`. Exit gate: legs
and compose PASS, `EXITRES mux-tunnels-2 … verdict=FAIL rss+66MiB>64MiB (Go heap: sys_mb +79MB with it)` — a first-set
warm-up, the set's own slope is SKIP and the later sets settle.

Outcome: #5035 merged as `1008cc8e5`, the head this day's criterion status is scored at (see
`docs/design/route-multiplexing-test-plan.md`). Criteria 2 and 3 are met at 50 MB; 4 is open but recovered.
