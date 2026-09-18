# e0d9e0430-smoke — composition v2

Commit under test: e0d9e0430 "feat(skysocks): a split chunk is sized from the object,
not from a fixed 4 MiB step" on top of #4992. No PR opened.
Ran: 2026-09-17 21:30Z-21:55Z, chain F (scratchpad/chainF.log).
Sets: mux-legs-2, mux-tunnels-2, mux-compose-T2xL2, mux-standby-8.

## Verdict rows — compose, paired vs 0371ab4b (15 rows, hash_ok=15)
    down 10  4.69 MB/s  x1.479 PASS      up 10  4.38  x0.613 FAIL
    down 50  6.47       x1.329 PASS      up 50  8.62  x0.857 FAIL
    down 100 5.88       x1.076 PASS
Object-sized chunks fixed the download side that composition v1 (027933c35-smoke) had
regressed: all three download cells beat the interleaved reference. Uploads still sat
below the 0.95 bar.

## Cut row (mux-standby-8)
Pool settled at 8 groups. Cut tp 95839ad0-b588-0b1d-8475-45fba6ae993c on rg 49185 at
5.192 s after 2097152 bytes; ttfb after 19.741 s — FAIL (want < 2), goodput
403920 -> 2389527 B/s. hashes 3/3; survivors 7/7, rg 49185 lost (only the cut group);
tunnel_promoted x1; no reorder wedge. 12 rows, hash_ok=12.

## Exit gate
All three sets PASS (compose rss 431456 -> 466660 kB, settled 425068, cpu 43.3 s over
374 s). Campaign slope SKIP, span 14.4 min.

Outcome: not merged — the failover ttfb was still 19.7 s, unchanged from composition
v1, and the upload cells did not move. Superseded by 91490ed9a-smoke (v3).
