# 120e1c31c-smoke — #4992 promoter smoke (first tip)

Commit under test: 120e1c31c "changelog: the promoter and the idle audition (#4992)"
— the #4992 branch (a standby tunnel that measures better swaps in; an idle one
auditions for a capacity sample) on top of #4991 (385138588).
Ran: 2026-09-17 19:30Z-19:43Z, chain B (scratchpad/chainB.log).
Sets: mux-legs-2, mux-tunnels-2, mux-standby-8.

## Verdict rows (bar mode)
    mux-legs-2     down 10  3.06 vs 5.00 FAIL (w/g 1.44)   up 10  4.48 vs 7.52 FAIL
    mux-legs-2     down 50  3.64 vs 5.43 FAIL              up 50  8.89 vs 10.32 FAIL
    mux-tunnels-2  down 10  2.89 vs 5.00 FAIL              up 10  4.73 vs 7.52 FAIL
    mux-tunnels-2  down 50  8.16 vs 5.43 PASS              up 50  9.06 vs 10.32 FAIL
    mux-standby-8  down 50  5.78 vs 5.43 PASS              up 50  8.98 vs 10.32 FAIL
50 MB down recovered here (8.16 MB/s on tunnels-2, carrier 2/2) versus 2.62 MB/s on
the 67dddb8be baseline.

## Cut row (mux-standby-8)
Pool settled at 8 groups. Cut tp f1012467-de9d-09a9-8b49-62a43945c24b on rg 49171 at
5.209 s after 4194304 bytes; ttfb after 3.724 s — FAIL (want < 2). hashes 3/3;
survivors 7/7, rg 49171 lost (only the cut group); tunnel_promoted x1; no reorder
wedge at either end. 12 rows, hash_ok=12.

## Exit gate
mux-tunnels-2 FAIL (rss +74 MiB > 64 MiB); mux-legs-2 PASS.

Outcome: superseded by 2e6d92b18-smoke, which re-ran the same branch after the
first-tick retirement fix; the branch merged as 128bdd0eb (#4992).
