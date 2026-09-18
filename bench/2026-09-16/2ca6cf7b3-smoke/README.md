# 2ca6cf7b3-smoke — merged develop after #5002/#5005 (chain O)

Commit under test: 2ca6cf7b3, merged develop carrying `proxy settings` (#5002) and the
2026-09-17 results docs (#5005) on top of #4995/#5004. This is the baseline the day's
live sweeps (chains P, Q, R) were run against — the rig was left on this deploy.
Ran: 2026-09-18 01:07Z–01:24Z, chain O (`scratchpad/chainO.log`).
Sets: mux-tunnels-2, mux-legs-2, mux-standby-8 (40 % cut), mux-degrade-tunnels-2.
Bar mode (no paired reference this chain) against the campaign20 bars.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-legs-2         down  10   5.22    5.51     2/2    1.16  bar     FAIL    ref-via-03f57e7c
mux-legs-2         up    10   5.02    7.52     3/3    1.01  bar     FAIL    ref-direct-stcpr
mux-legs-2         down  50   7.11    5.43     2/2    1.16  bar     PASS    ref-via-0371ab4b
mux-legs-2         up    50   5.77    10.32    3/3    1.00  bar     FAIL    ref-direct-stcpr
mux-standby-8      down  10   4.96    5.00     3/3    1.03  bar     FAIL    ref-direct-stcpr
mux-standby-8      up    10   4.03    7.52     3/3    1.00  bar     FAIL    ref-direct-stcpr
mux-standby-8      down  50   5.37    5.43     3/3    1.01  bar     FAIL    ref-via-0371ab4b
mux-standby-8      up    50   9.26    10.32    3/3    1.00  bar     FAIL    ref-direct-stcpr
mux-tunnels-2      down  10   4.78    5.00     2/2    1.02  bar     FAIL    ref-direct-stcpr
mux-tunnels-2      up    10   3.57    7.52     3/3    1.00  bar     FAIL    ref-direct-stcpr
mux-tunnels-2      down  50   7.08    5.43     2/2    1.01  bar     PASS    ref-via-0371ab4b
mux-tunnels-2      up    50   9.30    10.32    3/3    1.00  bar     FAIL    ref-direct-stcpr
```
Cut row (mux-standby-8): pool 8, cut tp 95839ad0-b588-0b1d-8475-45fba6ae993c on rg 49176
after 10080256 B; `ttfb_after_cut_s 1.139 PASS`, `promote_event tunnel_promoted x1 PASS`,
`survivors_kept 7/7 PASS`, `rg_ports_lost none PASS`, both reorder-wedge counters 0.
Degrade: 2/2 rows http=200, ttfb 1.795/1.737 s. EXITRES PASS on both sets.

Decided: the #5002 live-knob build is sound on the rig and becomes the fixed deploy the
day's knob sweeps run against; only the 50 MB download clears a bar without pairing.
