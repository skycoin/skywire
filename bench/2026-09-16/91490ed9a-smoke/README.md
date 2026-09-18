# 91490ed9a-smoke — composition v3 (2 MiB head-chunk cap)

Commit under test: 91490ed9a "fix(skysocks): a planned chunk is never larger than the
head chunk the writer waits for" on top of e0d9e0430 (composition v2). No PR opened.
Ran: 2026-09-17 22:23Z-22:50Z, chain I (scratchpad/chainI.log).
Sets: mux-legs-2, mux-tunnels-2, mux-compose-T2xL2, mux-standby-6.

## Verdict rows — compose (bar mode; no contemporaneous reference resolved, so the
set was measured UNPAIRED and verdict.sh fell back to the bar)
    down 10  0.94 MB/s  2/3 hashes  FAIL       up 10  4.92
    down 50  1.16       2/3 hashes  FAIL       up 50  8.76
    down 100 2.19
15 rows, hash_ok=13. Against composition v2's 4.69 / 6.47 / 5.88 MB/s on the same
cells this is a clear regression, and two download rows lost their hash.
mux-tunnels-2 down 10 also fell to 0.70 MB/s.

## Cut row (mux-standby-6)
Pool settled at 6 groups. Cut tp f1012467-de9d-09a9-8b49-62a43945c24b on rg 49192 at
5.191 s after 2097152 bytes; ttfb after 27.143 s — FAIL, the worst of the day.
hashes 3/3; survivors 7/7, rg 49192 lost (only the cut group); tunnel_promoted x1; no
reorder wedge. 12 rows, hash_ok=12.

## Exit gate
All three sets PASS (compose rss 422816 -> 458128 kB, settled 461024, cpu 47.9 s over
520 s).

Outcome: not merged — the 2 MiB head-chunk cap was a regression on every download cell
and was dropped. The composition line continued from e0d9e0430 as c302e1746-smoke.
