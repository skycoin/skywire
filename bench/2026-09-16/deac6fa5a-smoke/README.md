# deac6fa5a-smoke — int-D2 (audit fixes, merged from here)

Commit under test: deac6fa5a, merge of b79ae2299 into int-D2, adding "a retired
tunnel's refill waits one tick, and an audition never doubles up on a busy standby"
to the int-D content — the branches opened as #4996 and #4998.
Ran: 2026-09-17 21:55Z-22:10Z, chain G (scratchpad/chainG.log).
Sets: mux-legs-2, mux-tunnels-2, mux-standby-8.

## Verdict rows (bar mode)
    mux-legs-2     down 10  3.92 vs 5.00 FAIL   up 10  0.88 vs 7.52 FAIL (hash 2/3)
    mux-legs-2     down 50  7.29 vs 5.43 PASS   up 50  8.82 vs 10.32 FAIL
    mux-tunnels-2  down 10  4.72 vs 5.00 FAIL   up 10  4.92 vs 7.52 FAIL
    mux-tunnels-2  down 50  4.91 vs 5.43 FAIL   up 50  9.10 vs 10.32 FAIL
    mux-standby-8  down 10  5.13 vs 5.00 PASS   up 10  7.19 vs 7.52 FAIL
    mux-standby-8  down 50  4.89 vs 5.43 FAIL   up 50  8.60 vs 10.32 FAIL
The standby upload collapse of fe53f42dc-smoke (0.47 MB/s, w/g 0.02) is gone: 8.60
MB/s at 50 MB up with wire/good 1.00 and 3/3 hashes.

## Cut row (mux-standby-8)
Pool settled at 8 groups. Cut tp 95839ad0-b588-0b1d-8475-45fba6ae993c on rg 49174 at
5.220 s after 37744640 bytes; ttfb after 0.626 s — PASS. hashes 3/3; survivors 7/7,
no rg port lost or gained; tunnel_promoted x1; no reorder wedge at either end.
12 rows, hash_ok=12. Goodput 7230774 -> 17140364 B/s across the cut.

## Exit gate
mux-tunnels-2 PASS (rss +1.3 MiB), mux-legs-2 PASS.

Outcome: merged — #4996 as a8fe8d489 and #4998 as 872ebfe5e.
