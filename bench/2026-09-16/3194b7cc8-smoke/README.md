# 3194b7cc8-smoke — int-E (striped upload, first pass)

Commit under test: 3194b7cc8, merge of fix/sink-upload-session-lifecycle into int-E —
the branches later opened as #4997 (an upload to a cooperating sink is striped across
the tunnels in acked chunks and survives a dead tunnel) and #4999 (upload sessions
survive a slow chunk, a held chunk is 202), on top of #4992.
Ran: 2026-09-17 21:15Z-21:29Z, chain E (scratchpad/chainE.log).
Sets: mux-legs-2, mux-tunnels-2, mux-degrade-tunnels-2, mux-tunnels-2-up2 (INVALID).
## Verdict rows (bar mode)
    mux-tunnels-2  down 10  5.31 vs 5.00 PASS   down 50  5.46 vs 5.43 PASS
    mux-tunnels-2  up 10    6.44 vs 7.52 FAIL   up 50    9.60 vs 10.32 FAIL
    mux-legs-2     down 10  4.57 FAIL (w/g 1.21)  down 50  4.21 FAIL (w/g 1.50)
    mux-degrade-tunnels-2 up 50  1.37 vs 10.32 FAIL, hash 1/2, w/g 3.70
mux-tunnels-2-up2 INVALID: the two-upload sum needs two distinct reference routes and
neither slot resolved.

## Cut rows (mux-degrade-tunnels-2, 50 MB up, cut 40 %/3 s)
    t1  cut at 2.940 s after 28920166 B  ->  http=502, got 46985655, hash_ok=0,
        ttfb after 1.905 s, goodput 9836791 -> 432748 B/s
    t2  cut at 3.209 s after 29770792 B  ->  http=200, got 50000000, hash_ok=1,
        ttfb after 3.212 s, goodput 9277280 -> 761699 B/s
2 rows, hash_ok=1, cuts=2. The session was out of shape after each cut (the cut target
was not held) and had to be re-established; the set recorded 4 route groups when 2
were wanted.

## Exit gate
mux-tunnels-2 PASS (rss +18 MiB, settled 377816 kB).

Outcome: not merged — a cut mid-upload returned 502 with a short body. Fixed and
re-run as d6fecd1c3-smoke (int-E2).
