# e128db1ff-smoke — int-N: #5029 snub gating + #5030 acceptor park + #5012 gated

Binary `e128db1ff` (int-N) = develop `0db22347b` + #5029 (a head-of-line-blocked tunnel is not snubbed, snub events reach
the visor's mux-event log) + #5030 (an acceptor never parks a stalled leg on its own) + #5012 with grouping gated.
Chain AI, 2026-09-18 08:40Z–09:04Z (`scratchpad/chainAI.log`). Sets: mux-tunnels-2, mux-legs-2 (3 trials),
mux-compose-T2xL2 50 MB + 100 MB down with the usual pins, then the same cells with HOMOGENEOUS-latency pins in
[`homog/`](homog/); paired reference `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`, ceiling 11.23 / 9.55 MB/s.
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-compose-T2xL2  down  50   8.13    x1.001   3/3    1.20  paired  FAIL    best 8.61 saturates ceiling 9.55 — bar 0.95 x ceiling = 9.07
mux-compose-T2xL2  up    50   3.48    x0.338   3/3    1.14  paired  FAIL    vs better of t2 0.992 l2 0.817, no ceiling row
mux-compose-T2xL2  down  100  5.08    x0.565   3/3    1.21  paired  FAIL    NOCOMP w/g 1.21
mux-legs-2         down  10   5.18    x0.838   3/3    1.08  paired  FAIL    -
mux-legs-2         up    10   3.63    x0.500   3/3    1.46  paired  FAIL    - w/g 1.46
mux-legs-2         down  50   8.46    x0.999   3/3    1.08  paired  PASS    -
mux-legs-2         up    50   8.42    x0.817   3/3    1.09  paired  FAIL    -
mux-tunnels-2      down  10   5.29    x0.991   3/3    1.01  paired  PASS    -
mux-tunnels-2      up    10   2.74    x0.393   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  50   8.61    x1.094   3/3    1.00  paired  PASS    -
mux-tunnels-2      up    50   9.37    x0.992   3/3    1.00  paired  PASS    -
```
All 33 rows hash-clean. The homogeneous-pin arm scored `down 50 7.35 x0.755`, `up 50 7.78 x0.756`, `down 100 5.99 x0.613`
— all NOCOMP, so the composition shortfall is not a latency-spread artefact of the pin set. Direction: `mux-tunnels-2 fwd
95839ad0 lowest-latency PASS=8 FAIL=0`, `mux-legs-2 fwd fdab37dd other PASS=4 FAIL=4`. Exit gate all sets PASS
(`mux-compose-T2xL2 d_rss=44068 KiB cpu=33.0s wall=283s`; homog `d_rss=37020 KiB cpu=32.1s`).

Outcome: all three merged — #5029 as `1545f15f0`, #5030 as `14a8b5354`, #5012 as `da1709895` (its fifth run, first merge).
The 10 MB upload cells (t2 x0.393, l2 x0.500) and the composition uploads stayed open and drove chains AJ and AK.
