# 0290ff96a-smoke — int-F2 (merged from here)

Commit under test: 0290ff96a, merge of c7ab74a35 into int-F2 — the int-F content with
the chunk first-byte bound withdrawn, leaving the object-sized split chunk, the
close-aware chunk refetch resuming from the received offset, and "a route that died
within seconds of its dial is not re-picked by the next diversify search"; opened as
#4995 and #5004.
Ran: 2026-09-18 00:29Z-00:51Z, chain N (scratchpad/chainN.log). The rig needed one
retry to reach 6/6 hop-1 stcpr.
Sets: mux-legs-2, mux-tunnels-2, mux-compose-T2xL2, mux-standby-8.
## Verdict rows
Smoke, bar mode — 4 of 8 rows PASS, the best of the day:
    mux-legs-2     down 10  6.02 vs 5.51 PASS   down 50  8.52 vs 8.05 PASS
    mux-tunnels-2  down 10  5.83 vs 5.51 PASS   down 50  8.79 vs 8.05 PASS
    mux-tunnels-2  up 50   10.17 vs 9.95 PASS   up 10    6.43 vs 6.81 FAIL
Compose, paired vs 0371ab4b (15 rows, hash_ok=15): down 10 x0.981, up 10 x0.627,
down 50 x0.462, up 50 x0.913, down 100 x0.661. The 50 MB and 100 MB download cells
stayed under the bar; the smoke sets did not.

## Cut row (mux-standby-8)
Pool settled at 8 groups. Cut tp f1012467-de9d-09a9-8b49-62a43945c24b on rg 49184 at
5.213 s after 6086656 bytes; ttfb after 0.682 s — PASS. hashes 3/3; survivors 7/7, no
rg port lost or gained; tunnel_promoted x2; no reorder wedge at either end. 12 rows,
hash_ok=12. Goodput 1167592 -> 13416848 B/s across the cut. Every assert PASS.

## Exit gate
All three sets PASS (compose rss 443876 -> 477236 kB, settled 424432, cpu 38.2 s over
325 s).

Outcome: merged — #4995 as ff081323f and #5004 as 6168f189c.
