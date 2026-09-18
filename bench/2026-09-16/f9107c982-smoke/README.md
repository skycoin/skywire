# f9107c982-smoke — int-H: #5014 spread + #5013 snub/depth (chain X)

Commit under test: f9107c982, int-H = develop 21003f4ee + #5014 (spread policy:
capacity-proportional chunk assignment, max share per route, min routes, endgame) +
#5013 (a silent tunnel is snubbed and its chunks re-issued; per-tunnel depth from BDP).
This is the first run of the criterion-10 `mux-spread-3` set.
Ran: 2026-09-18 03:19Z–03:35Z, chain X (`scratchpad/chainX.log`).
Sets: mux-tunnels-2, mux-degrade-tunnels-2 (produced no rows), mux-standby-9 (40 % cut),
mux-spread-3, plus `direction.sh`.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-spread-3       down  50   5.13    -        2/2    1.02  bar     NOBAR   no 50 MB down reference in bench/2026-09-16/f9107c982-smoke
mux-spread-3       up    50   9.43    -        3/3    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/f9107c982-smoke
mux-standby-9      down  10   5.61    -        3/3    1.00  bar     NOBAR   no 10 MB down reference in bench/2026-09-16/f9107c982-smoke
mux-standby-9      up    10   4.37    -        3/3    1.00  bar     NOBAR   no 10 MB up reference in bench/2026-09-16/f9107c982-smoke
mux-standby-9      down  50   6.48    -        3/3    1.04  bar     NOBAR   no 50 MB down reference in bench/2026-09-16/f9107c982-smoke
mux-standby-9      up    50   9.23    -        3/3    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/f9107c982-smoke
mux-tunnels-2      down  10   4.92    -        2/2    1.01  bar     NOBAR   no 10 MB down reference in bench/2026-09-16/f9107c982-smoke
mux-tunnels-2      up    10   2.53    -        3/3    1.00  bar     NOBAR   no 10 MB up reference in bench/2026-09-16/f9107c982-smoke
mux-tunnels-2      down  50   6.86    -        2/2    1.02  bar     NOBAR   no 50 MB down reference in bench/2026-09-16/f9107c982-smoke
mux-tunnels-2      up    50   10.12   -        3/3    1.00  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/f9107c982-smoke
```
Every row is NOBAR and every paired cell is missing: three pins in the scratchpad had been
overwritten with a stub `TpID`, so the paired references and the degrade set were silently
invalid (post-mortem in `scratchpad/BRIEF.md`). Spread asserts: `routes_active min 2 FAIL`,
`max_share 0.6690 FAIL`, both ratios "no paired rows" FAIL, `hashes 5/5 PASS`,
`spread_policy applied PASS` (knobs landed in 8 s). Cut row: pool 9,
`ttfb_after_cut_s 1.187 PASS`, `hashes_after_cut 3/3 PASS`, `promote_event none FAIL`.
EXITRES PASS.

Decided: the spread set and the live spread knobs work end to end, but the run is not
scoreable — it produced the pin-validation rule (#5019 rejects stub pins loudly) and the
rule that chain scripts must keep the unfiltered runner output.
