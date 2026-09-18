# 845dd383e-smoke — #5010 lowest-latency forward leg (chain T)

Commit under test: 845dd383e, PR #5010 ("forward traffic takes the lowest-latency leg when
no leg is direct; per-leg byte counters at the exit"). This is the build that makes
criterion 5 measurable from both ends.
Ran: 2026-09-18 02:52Z–03:05Z, chain T (`scratchpad/chainT.log`).
Sets: mux-tunnels-2, mux-legs-2, 3 trials each, paired vs 0371ab4b, plus `direction.sh`.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-legs-2         down  10   3.67    x0.634   3/3    1.47  paired  FAIL    - w/g 1.47
mux-legs-2         up    10   4.01    x0.556   3/3    1.01  paired  FAIL    -
mux-legs-2         down  50   8.62    x1.093   3/3    1.02  paired  PASS    -
mux-legs-2         up    50   8.51    x0.899   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  10   4.76    x0.797   3/3    1.00  paired  FAIL    -
mux-tunnels-2      up    10   0.00    x3.000   1/3    0.00  paired  FAIL    - hash 1/3
mux-tunnels-2      down  50   7.98    x1.000   3/3    1.01  paired  PASS    -
mux-tunnels-2      up    50   10.27   x1.005   3/3    1.00  paired  PASS    -
```
Direction (the point of the run):
```
mux-legs-2     rows=12  fwd fdab37dd lowest-latency PASS=9 FAIL=1 INFO=2 | rev fanout<=2 PASS=3 FAIL=0 INFO=9 | flips=2 @r4,5
mux-tunnels-2  rows=12  fwd b414796d direct         PASS=10 FAIL=0 INFO=2 | rev fanout<=2 PASS=1 FAIL=2 INFO=9 | flips=1 @r5
```
EXITRES PASS on both sets.

Decided: #5010 fixes the criterion-5 counter-case — legs-2's forward leg now goes to the
lowest-latency leg 9 rows of 12 where every earlier run failed most rows — and merged as
48872035e.
