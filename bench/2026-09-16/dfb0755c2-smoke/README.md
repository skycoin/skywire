# dfb0755c2-smoke — int-L: #5012 with per-SACK, park-as-trial and an evidence floor (chain AF)

Commit under test: dfb0755c2, int-L = develop + PR #5012 at 0512a6044 (shared-bottleneck
detection per SACK, park as a goodput trial, and no ruling below an aggregate-rate floor).
Ceiling-aware verdicts (`ceiling.tsv` from chain Z).
Ran: 2026-09-18 06:51Z–07:14Z, chain AF (`scratchpad/chainAF.log`).
Sets: mux-tunnels-2, mux-legs-2 (5 down / 3 up), mux-compose-T2xL2 (2x2, 10/50 MB down+up,
100 MB down), plus `direction.sh`. Paired vs 0371ab4b.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  10   1.71    x0.332   5/5    1.29  paired  FAIL    vs t2 0.905 l2 0.869 w/g 1.29
mux-compose-T2xL2  up    10   1.30    x0.224   2/3    1.05  paired  FAIL    vs t2 1.184 l2 0.653 hash 2/3
mux-compose-T2xL2  down  50   2.74    x0.386   5/5    1.14  paired  FAIL    best 9.37 saturates ceiling 9.55 — bar 0.95 x ceiling = 9.07
mux-compose-T2xL2  up    50   2.85    -        0/3    0.46  bar     FAIL    no 50 MB up reference in bench/2026-09-16/dfb0755c2-smoke hash 0/3
mux-compose-T2xL2  down  100  5.89    x0.646   5/5    1.14  paired  FAIL    NOCOMP
mux-legs-2         down  10   5.03    x0.869   5/5    1.15  paired  FAIL    -
mux-legs-2         up    10   4.66    x0.653   3/3    1.01  paired  FAIL    -
mux-legs-2         down  50   5.96    x0.703   5/5    1.09  paired  FAIL    -
mux-legs-2         up    50   10.26   x0.991   3/3    1.00  paired  PASS    -
mux-tunnels-2      down  10   5.04    x0.905   5/5    1.00  paired  FAIL    -
mux-tunnels-2      up    10   7.20    x1.184   3/3    1.00  paired  PASS    -
mux-tunnels-2      down  50   9.37    x1.126   5/5    1.01  paired  PASS    -
mux-tunnels-2      up    50   10.54   x1.020   3/3    1.00  paired  PASS    -
```
tunnels-2 holds (50 MB down x1.126, 50 MB up x1.020), but the composition set collapsed:
17 of 21 rows hash-clean, all three 50 MB uploads failed their hash, and every download cell
came in between x0.33 and x0.65 at w/g up to 1.29. EXITRES PASS;
`EXITRES-SLOPE ... FAIL drift +87.8 MB` on the compose set.

Decided: the evidence floor still does not recover the second leg (legs-2 50 MB down
x0.703), and the composition uploads confirm the short-chunk 400 is terminal for a striped
upload — #5012 was reworked again (demotion off by default) and #5027 written for the
retry, both carried into chain AH.
