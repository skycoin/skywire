# a6a42506f-smoke — int-E3 (striped upload, route lottery)

Commit under test: a6a42506f, merge of 416a27879 into int-E3 — the int-E2 content plus
"the upload window defaults to 64 MiB, so a striped client keeps its pipeline"; the
branches later opened as #4997 and #4999.
Ran: 2026-09-17 22:50Z-23:10Z, chain J (scratchpad/chainJ.log).
Sets: mux-legs-2, mux-tunnels-2, mux-tunnels-2-up2, mux-degrade-tunnels-2.
## Verdict rows (paired vs 0371ab4b)
    mux-tunnels-2  down 10  0.72 MB/s  x0.319 FAIL   up 10  4.96  x0.934 FAIL
    mux-tunnels-2  down 50  1.78       x0.600 FAIL   up 50  8.08  x0.783 FAIL
    mux-legs-2     down 10  2.80       x0.635 FAIL   up 10  4.74  x0.658 FAIL
    mux-legs-2     down 50  4.48       x1.003 PASS   up 50  8.89  x0.860 FAIL
The per-row ratios inside a single cell ranged from 0.110 to 7.923 on tunnels-2 — the
route lottery, not a property of the build. Two concurrent uploads:
    trial 1  5.30 + 5.11 = 10.41 vs ref_sum 18.51  ->  ratio 0.562
    trial 2  5.43 + 5.47 = 10.90 vs ref_sum 19.25  ->  ratio 0.566

## Cut rows (mux-degrade-tunnels-2, 50 MB up, cut 40 %/3 s)
    t1  cut at 2.808 s after 26428042 B  ->  http=200, hash_ok=1, ttfb after 1.759 s,
        goodput 9411696 -> 4898578 B/s
    t2  cut at 2.810 s after 32530043 B  ->  http=200, hash_ok=1, ttfb after 1.776 s,
        goodput 11576528 -> 3291871 B/s
2 rows, hash_ok=2, cuts=2. The set recorded 5 route groups when 2 were wanted.

## Exit gate
mux-tunnels-2 FAIL (rss +94 MiB > 64 MiB, sys +103 MB > 96 MB); mux-legs-2 PASS;
mux-tunnels-2-up2 PASS.

Outcome: not merged — the exit gate failed and the throughput rows could not be read
through the route lottery. Re-run as 0186db249-smoke (int-E4), which merged.
