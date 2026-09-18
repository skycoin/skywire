# 0cbbf925a-smoke — int-K: #5023 frontier streaming + #5022 audition (chain AC)

Commit under test: 0cbbf925a, int-K = develop 73f713b89 + #5023 (the range splitter streams
the frontier chunk as it arrives, so a cut costs one detection, not one chunk) + #5022 (a
standby's audition never takes the entry stream) + #5014 spread (e22c03e04) + #5013 snub.
Ceiling-aware verdicts (`ceiling.tsv` from chain Z).
Ran: 2026-09-18 05:33Z–05:55Z, chain AC (`scratchpad/chainAC.log`).
Sets: mux-tunnels-2 (5 down / 3 up), mux-degrade-tunnels-2, mux-standby-8 (active cut),
mux-spread-3, plus `direction.sh`. Paired vs 0371ab4b.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-degrade-tunnels-2 up    50   9.26    -        2/2    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/0cbbf925a-smoke
mux-spread-3       down  50   6.76    x0.866   5/5    1.00  paired  FAIL    -
mux-spread-3       up    50   9.78    x0.951   3/3    1.00  paired  PASS    -
mux-standby-8      down  10   2.05    -        3/3    1.00  bar     NOBAR   no 10 MB down reference in bench/2026-09-16/0cbbf925a-smoke
mux-standby-8      up    10   5.21    -        3/3    1.00  bar     NOBAR   no 10 MB up reference in bench/2026-09-16/0cbbf925a-smoke
mux-standby-8      down  50   7.28    -        3/3    1.03  bar     NOBAR   no 50 MB down reference in bench/2026-09-16/0cbbf925a-smoke
mux-standby-8      up    50   9.31    -        3/3    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/0cbbf925a-smoke
mux-tunnels-2      down  10   5.28    x0.949   5/5    1.00  paired  FAIL    -
mux-tunnels-2      up    10   6.45    x0.969   3/3    1.00  paired  PASS    -
mux-tunnels-2      down  50   9.65    x1.106   5/5    1.01  paired  PASS    -
mux-tunnels-2      up    50   10.54   x1.037   3/3    1.00  paired  PASS    -
```
Criterion 10 (`mux-spread-3.assert.tsv`) — the run that meets it:
`routes_active min 3 max 3 PASS`, `max_share 0.4410 (row 2, leg f1012467) <= 0.484 PASS`,
`ratio_50down 0.866 PASS`, `ratio_50up 0.951 PASS`, `hashes 8/8 PASS`, `spread_policy applied PASS`.
Cut row (criterion 6): `cut_target ... role active PASS`, `survivors_kept 7/7 PASS`,
`promote_event tunnel_promoted x1 PASS`, `ttfb_after_cut_s 0.643 PASS`, both reorder-wedge
counters 0, `hashes_after_cut 3/3 PASS`. Degrade 2/2 http=200, ttfb 1.765/1.758 s.
EXITRES PASS.

Decided: the day's best run — #5023 turns the active-tunnel cut into 0.643 s to first byte
(criterion 6 MET) and the spread policy meets criterion 10 outright. Both merged
(8a8b1daf9, 5505f18a7, 5fec4edb4, 8e88caf88).
