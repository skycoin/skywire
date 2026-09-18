# d6fecd1c3-smoke — int-E2 (striped upload, degrade fixed)

Commit under test: d6fecd1c3, merge of c58883cce into int-E2 — the int-E content plus
"the striped upload leaves the sink room to take a re-send" and "the sink names the
held chunk it dropped"; the branches later opened as #4997 and #4999.
Ran: 2026-09-17 22:09Z-22:23Z, chain H (scratchpad/chainH.log).
Sets: mux-legs-2, mux-tunnels-2, mux-degrade-tunnels-2, mux-tunnels-2-up2 (INVALID).

## Verdict rows (paired vs 0371ab4b)
    mux-tunnels-2  down 10  5.44 MB/s  x2.451 PASS   up 10  3.38  x0.493 FAIL
    mux-tunnels-2  down 50  4.91       x0.682 FAIL   up 50  8.04  x0.787 FAIL
    mux-legs-2     down 10  2.48       x0.797 FAIL   up 10  4.69  x0.653 FAIL
    mux-legs-2     down 50  4.69       x0.763 FAIL   up 50  8.84  x0.857 FAIL
    mux-degrade-tunnels-2  up 50  4.79 MB/s, 2/2 hashes, no bar available
Throughput was low across the board — six of eight paired cells under 0.80.
mux-tunnels-2-up2 INVALID again: only one reference route resolved ('0371ab4b').

## Cut rows (mux-degrade-tunnels-2, 50 MB up, cut 40 %/3 s)
    t1  cut at 4.269 s after 12526540 B  ->  http=200, hash_ok=1, ttfb after 1.748 s,
        goodput 2934303 -> 2816495 B/s
    t2  cut at 4.192 s after 25181575 B  ->  http=200, hash_ok=1, ttfb after 1.778 s,
        goodput 6007055 -> 7671847 B/s
2 rows, hash_ok=2, cuts=2. The 502 of 3194b7cc8-smoke is fixed; both cut uploads now
complete with a good hash and a sub-2 s first byte.

## Exit gate
mux-tunnels-2 PASS (rss +65 MiB, settled 416248 kB), mux-legs-2 PASS.

Outcome: not merged — the degrade defect was fixed but the throughput was too low to
ship on. Iterated as a6a42506f-smoke (int-E3) and 0186db249-smoke (int-E4).
