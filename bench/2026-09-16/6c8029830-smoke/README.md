# 6c8029830-smoke — int-O: #5031 upload placement + #5032 leg-basis retransmit

Binary `6c8029830` (int-O) = develop `1545f15f0` + #5031 (striped-upload chunks placed by upload capacity and RTT, an
unmeasured tunnel is not assumed fastest, the tail chunk never goes to the slow tunnel) + #5032 (a SACK-requested
retransmit honours the sending leg's delay basis). Chain AJ, 2026-09-18 09:31Z–09:58Z (`scratchpad/chainAJ.log`).
Sets: mux-tunnels-2, mux-legs-2 (5/3 trials), mux-compose-T2xL2 10/50 MB + 100 MB down; paired reference
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`, ceiling-aware (uplink 11.23, downlink 9.55 MB/s).
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  10   3.92    x0.709   5/5    1.17  paired  FAIL    vs t2 0.901 l2 0.854
mux-compose-T2xL2  up    10   0.27    x0.041   3/3    1.53  paired  FAIL    vs t2 0.861 l2 0.677 w/g 1.53
mux-compose-T2xL2  down  50   6.99    x0.930   5/5    1.03  paired  FAIL    vs better of t2 0.773 l2 1.125 (5 % band); best 8.16 < 0.9 x ceiling 9.55
mux-compose-T2xL2  up    50   2.43    x0.244   3/3    1.09  paired  FAIL    vs better of t2 0.806 l2 0.803, no ceiling row
mux-compose-T2xL2  down  100  8.57    x1.055   5/5    1.02  paired  PASS    NOCOMP
mux-legs-2         down  10   5.35    x0.854   5/5    1.08  paired  FAIL    -
mux-legs-2         up    10   4.89    x0.677   3/3    1.26  paired  FAIL    - w/g 1.26
mux-legs-2         down  50   8.16    x1.125   5/5    1.00  paired  PASS    -
mux-legs-2         up    50   8.29    x0.803   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  10   4.96    x0.901   5/5    1.00  paired  FAIL    -
mux-tunnels-2      up    10   5.47    x0.861   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  50   6.31    x0.773   5/5    1.04  paired  FAIL    -
mux-tunnels-2      up    50   8.29    x0.806   3/3    1.00  paired  FAIL    -
```
All 53 rows hash-clean, the first fully clean chain of the day, and the 100 MB download cleared the ceiling-aware bar at x1.055 PASS.
The composition uploads did not recover — 10 MB up 0.27 (x0.041) is the day's floor — and every tunnels-2 cell slipped below the bar, so #5031's placement alone does not reach the upload gap. Direction: `mux-tunnels-2 fwd b414796d
direct+lowest-latency PASS=5 FAIL=0`, `mux-legs-2 fwd fdab37dd other PASS=2 FAIL=8`. Exit gate all sets PASS, but
`EXITRES-SLOPE 3717 KiB/min over 21.9 min verdict=FAIL drift +79.6 MB at 3.6 MB/min > 2.0 MB/min` on the compose set.

Outcome: both merged — #5031 as `15e12e619`, #5032 as `d23f8b3d6`. The remaining upload spill was diagnosed as forward
traffic leaving its confined leg, which became #5035 and chain AK ([`908a98bcd-smoke`](../908a98bcd-smoke/)).
