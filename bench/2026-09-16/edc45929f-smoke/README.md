# edc45929f-smoke — #5012 v1, SBD per SACK + RACK per-leg delay (chain W)

Commit under test: edc45929f, the first version of PR #5012 ("shared-bottleneck detection
per SACK, RACK using per-leg end-to-end delay").
Ran: 2026-09-18 03:08Z–03:18Z, chain W (`scratchpad/chainW.log`).
Sets: mux-compose-T2xL2 (2x2, 50 + 100 MB down, 2 trials, paired vs 0371ab4b) and
mux-legs-2 (3 trials), plus `direction.sh` and the exit-side retransmit counters.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  50   7.21    x0.944   2/2    1.02  paired  FAIL    NOCOMP
mux-compose-T2xL2  down  100  5.41    x0.726   2/2    0.99  paired  FAIL    NOCOMP
mux-legs-2         down  10   4.69    -        3/3    1.09  bar     NOBAR   no 10 MB down reference in bench/2026-09-16/edc45929f-smoke
mux-legs-2         up    10   4.71    -        3/3    1.00  bar     NOBAR   no 10 MB up reference in bench/2026-09-16/edc45929f-smoke
mux-legs-2         down  50   3.75    -        3/3    1.00  bar     NOBAR   no 50 MB down reference in bench/2026-09-16/edc45929f-smoke
mux-legs-2         up    50   8.86    -        3/3    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/edc45929f-smoke
```
Exit retransmits over the compose set stayed flat (`retx_sent 119 -> 251`,
`retx_req_sack 93 -> 191`) — the per-SACK change does bound the storm. But the log carries
`shared bottleneck: co-bottlenecked with a kept active leg (one pipe, not two)` twice, and
legs-2's 50 MB download fell to 3.75 MB/s. Direction: legs-2 11 of 12 forward rows FAIL.
EXITRES PASS on both sets.

Decided: the retransmit side of #5012 is right, but its park verdict costs the second leg —
the reason the PR was reworked twice more (chains AB, AE, AF) and finally landed with the
demotion off by default.
