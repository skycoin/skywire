# 1647ae058-smoke — #5008 object-sized upload chunk (chain S)

Commit under test: 1647ae058, PR #5008 ("a striped upload's chunk is sized from the object,
under the `upload.chunk_bytes` ceiling") on develop 38e914344.
Ran: 2026-09-18 02:22Z–02:41Z, chain S (`scratchpad/chainS.log`).
Sets: mux-tunnels-2, mux-legs-2, mux-tunnels-2-up2, mux-degrade-tunnels-2. Paired vs
0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13.

## Verdict rows
```
set                dir   MB   median  bar/ratio ok/n   w/g   mode    verdict note
mux-degrade-tunnels-2 up    50   6.95    -        2/2    1.18  bar     NOBAR   no 50 MB up reference in bench/2026-09-16/1647ae058-smoke
mux-legs-2         down  10   4.83    x1.335   2/2    1.04  paired  PASS    -
mux-legs-2         up    10   4.59    x0.641   3/3    1.01  paired  FAIL    -
mux-legs-2         down  50   5.02    x1.041   2/2    1.11  paired  PASS    -
mux-legs-2         up    50   7.87    x0.767   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  10   5.15    x1.082   2/2    1.03  paired  PASS    -
mux-tunnels-2      up    10   4.57    x0.817   3/3    1.00  paired  FAIL    -
mux-tunnels-2      down  50   3.55    x0.956   2/2    1.05  paired  PASS    -
mux-tunnels-2      up    50   8.55    x0.916   3/3    1.00  paired  FAIL    -
mux-tunnels-2-up2  up    50   4.39    -        4/4    -     bar     NOBAR   no 50 MB up reference in bench/2026-09-16/1647ae058-smoke
mux-tunnels-2-up2  two concurrent uploads: sum 8.70 vs ref sum 17.70, median ratio x0.495 over 2 trial(s), 2 hash-clean -> FAIL
```
Two-upload trials: `4.07 + 4.39 = 8.46` vs ref sum 18.73 (x0.452) and
`4.55 + 4.39 = 8.94` vs 16.66 (x0.537). Degrade: 2/2 http=200, ttfb after the cut
3.725 s and 1.852 s. EXITRES PASS on all three sets.

Decided: object-sized chunks do not by themselves close the upload gap — two concurrent
uploads still sum to half the reference sum — so #5008 merged as a correctness step
(21003f4ee) while the upload work moved to the striping and snub PRs.
