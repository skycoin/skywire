# 027933c35-smoke — composition v1

Commit under test: 027933c35 "lint: US spelling in the new composition files" — the
first composition branch (burst too short to amortise the legs' ramp rides one leg;
exit stream reused at a clean HTTP boundary; idle leg parks its send window; split
chunk sized from the object) on top of #4992 (128bdd0eb). No PR opened.
Ran: 2026-09-17 20:23Z-20:56Z, chain C (scratchpad/chainC.log).
Sets: mux-legs-2, mux-tunnels-2, mux-compose-T2xL2, mux-standby-6.

## Verdict rows — compose, paired vs 0371ab4b (15 rows, hash_ok=15)
    down 10  1.69 MB/s  x0.623 FAIL      up 10  2.80  x0.401 FAIL
    down 50  5.11       x1.375 PASS      up 50  8.53  x0.831 FAIL
    down 100 3.38       x0.934 FAIL
10 MB down and up both collapsed against the interleaved reference; the 100 MB cell
missed the 0.95 bar. mux-legs-2 down 50 came in at 2.17 MB/s with wire/good 1.66.

## Cut row (mux-standby-6)
Pool settled at 6 groups. Cut tp f1012467-de9d-09a9-8b49-62a43945c24b on rg 49190 at
5.198 s after 2097152 bytes; ttfb after 19.336 s — FAIL (want < 2), goodput
403454 -> 2299373 B/s. hashes 3/3; survivors 6/6, no rg port lost or gained;
tunnel_promoted x1; no reorder wedge. 12 rows, hash_ok=12.

## Exit gate
All three sets PASS (compose rss 434932 -> 485204 kB, settled 431900, cpu 49.2 s over
467 s). Campaign slope SKIP, span 16.7 min under the 20 min minimum.

Outcome: not merged — the 10 MB cells regressed and the failover ttfb was 19.3 s.
Superseded by e0d9e0430-smoke (composition v2).
