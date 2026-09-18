# c302e1746-smoke — composition v4 (pool-3 snapshot)

Commit under test: c302e1746 "fix(skysocks): a chunk on a closed group fails at the
close and its refetch resumes from the received offset", on top of the object-sized
split chunk and the merged #4996/#4998. No PR opened.
Ran: 2026-09-17 23:10Z-23:33Z, chain K (scratchpad/chainK.log).
Sets: mux-legs-2, mux-tunnels-2, mux-compose-T2xL2, mux-standby-3.
## Verdict rows — compose, paired vs 0371ab4b (15 rows, hash_ok=15)
    down 10  4.33 MB/s  x0.866 (rows 0.942 / 0.866 / 0.688)
    up 10    4.57       x0.638 (rows 0.638 / 0.068 / 0.714)
    down 50  6.34       x1.349 (rows 0.427 / 1.349 / 1.045)
    up 50    9.05       x2.025 (rows 0.126 / 2.025 / 0.904)
    down 100 6.55       x1.071 (rows 0.811 / 1.225 / 1.071)
Row-to-row spread inside a cell spanned 0.068 to 2.025, so no cell verdict here is
worth much; the medians are the readable part.

## Cut row (mux-standby-3)
The pool settled at only 3 route groups (quiet 20 s) — the smallest of the day, which
is what makes this a pool-3 snapshot rather than a failover measurement. Cut tp
95839ad0-b588-0b1d-8475-45fba6ae993c on rg 49185 at 5.211 s after 38023168 bytes.
ttfb after the cut: no byte arrived after the cut, so the row is unmeasurable — FAIL.
hashes 3/3; survivors 3/3, no rg port lost; tunnel_promoted x2; no reorder wedge.
12 rows, hash_ok=12.

## Exit gate
All three sets PASS (compose rss 413460 -> 500520 kB, settled 451064, cpu 49.8 s over
455 s).

Outcome: not merged — ttfb after the cut was unmeasurable and the pool only reached 3.
The chunk work continued into int-F (27bd7a0d2-smoke) and int-F2 (0290ff96a-smoke).
