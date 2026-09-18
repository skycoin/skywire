# 195b1094c-sbdsweep — is #5012's park costing the second leg? (chain AD)

Binary: 195b1094c (the chain AB deploy reused; no redeploy). A two-arm live sweep of the
shared-bottleneck detector on mux-legs-2, 50 MB, 5 down / 3 up, paired vs 0371ab4b:
`sbd-off/` sets `--sbd-min-samples 1000000` on **both** ends (parking effectively off),
`sbd-default/` leaves the shipped 4.
Ran: 2026-09-18 05:21Z–05:27Z, chain AD (`scratchpad/chainAD.log`).

## Verdict rows
```
--- sbd-off
mux-legs-2         down  50   9.30    x1.130   5/5    1.02  paired  PASS    -
mux-legs-2         up    50   8.93    x0.864   3/3    1.01  paired  FAIL    -
park events: 1
--- sbd-default
mux-legs-2         down  50   6.80    x0.862   5/5    1.02  paired  FAIL    -
mux-legs-2         up    50   8.93    x0.882   3/3    1.00  paired  FAIL    -
park events: 2
```
Per-row ratios, sbd-off: 50down 1.130 1.122 1.094 1.153 1.241, 50up 0.990 0.757 0.864.
sbd-default: 50down 0.862 0.816 0.621 1.144 1.050, 50up 1.926 0.882 0.878. 8/8 hashes in
both arms.

Decided: **two legs do aggregate** — with parking off, legs-2 runs x1.13 on the 50 MB
download and every one of the five trials is above 1.09; with the shipped detector the same
build drops to x0.862. #5012's park verdict is the cost, and the PR was reworked to a
goodput trial (chain AE) then to demotion-off-by-default (chain AH).
