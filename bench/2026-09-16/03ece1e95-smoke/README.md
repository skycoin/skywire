# 03ece1e95-smoke — int-I: #5018 intake worker + #5014 spread + #5013 (chain AA)

Commit under test: 03ece1e95, int-I = develop 0ab0d9755 (incl. #5017 blackout capture and
#5019 scope/pins/cut fixes) + #5018 (per-route-group inbound intake worker) + #5014 spread
(amended fb1b90dad) + #5013 snub/depth. First run with valid pins after the chain X
post-mortem.
Ran: 2026-09-18 04:35Z–04:54Z, chain AA (`scratchpad/chainAA.log`).
Sets: mux-tunnels-2 (5 down / 3 up), mux-degrade-tunnels-2, mux-standby-8 (40 % cut on an
active tunnel), mux-spread-3, plus `direction.sh`. Paired vs 0371ab4b.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-degrade-tunnels-2 up    50   6.82    -        2/2    1.34  bar     FAIL    no 50 MB up reference in bench/2026-09-16/03ece1e95-smoke w/g 1.34
mux-spread-3       down  50   5.96    x0.928   5/5    1.03  paired  FAIL    -
mux-spread-3       up    50   7.62    x0.931   3/3    1.00  paired  FAIL    -
mux-standby-8      down  10   4.69    -        3/3    1.01  bar     NOBAR   no 10 MB down reference in bench/2026-09-16/03ece1e95-smoke
mux-standby-8      up    10   4.70    -        3/3    1.00  bar     NOBAR   no 10 MB up reference in bench/2026-09-16/03ece1e95-smoke
mux-standby-8      down  50   5.70    -        3/3    1.03  bar     NOBAR   no 50 MB down reference in bench/2026-09-16/03ece1e95-smoke
mux-standby-8      up    50   9.25    -        3/3    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/03ece1e95-smoke
mux-tunnels-2      down  10   5.09    x0.872   5/5    1.00  paired  FAIL    -
mux-tunnels-2      up    10   5.84    x0.837   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  50   7.24    x0.850   5/5    1.02  paired  FAIL    -
mux-tunnels-2      up    50   9.55    x0.924   3/3    1.00  paired  FAIL    -
```
Spread asserts: `routes_active min 3 max 4 PASS`, `max_share 0.9580 FAIL`,
`ratio_50down 0.928 PASS`, `ratio_50up 0.931 PASS`, `hashes 8/8 PASS`. Cut row:
`cut_target ... role active PASS`, `promote_event tunnel_promoted x1 PASS`,
`ttfb_after_cut_s 6.784 FAIL`. Degrade 2/2 http=200, ttfb 1.737/1.837 s. EXITRES PASS.

Decided: valid pins restore paired scoring and the cut now targets an active tunnel, but
the cut costs a whole chunk (6.8 s to first byte) — the finding that #5023's streaming
frontier was written against.
