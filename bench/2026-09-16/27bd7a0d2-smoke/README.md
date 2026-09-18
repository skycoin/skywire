# 27bd7a0d2-smoke — int-F (chunk first-byte bound, withdrawn)

Commit under test: 27bd7a0d2, merge of 944767746 into int-F — the chunked download
work plus "a chunk that receives no byte is abandoned and refetched on another
tunnel", on top of the merged #5001. The first-byte bound is the change under test
here; it was not opened as a PR.
Ran: 2026-09-18 00:01Z-00:25Z, chain M (scratchpad/chainM.log).
Sets: mux-legs-2, mux-tunnels-2, mux-compose-T2xL2, mux-standby-8.
## Verdict rows — compose, paired vs 0371ab4b (15 rows, hash_ok=15)
    down 10  3.46 MB/s  rows 1.083 / 0.704 / 2.746
    up 10    4.33       rows 0.115 / 0.840 / 0.749
    down 50  5.54       rows 0.775 / 0.727 / 0.981
    up 50    9.47       rows 0.567 / 1.391 / 0.927
    down 100 4.98       rows 0.791 / 0.428 / 0.586
The 100 MB cell was the clearest signal: all three rows under 0.80 against the
interleaved reference, versus 1.185 / 1.054 / 1.076 on composition v2.
## Cut row (mux-standby-8)
Pool settled at 8 groups. Cut tp f1012467-de9d-09a9-8b49-62a43945c24b on rg 49189 at
5.155 s after 0 bytes before the cut; the transfer returned http=200 with only 338902
of 50000000 bytes and hash_ok=0. hashes after the cut 2/3 — FAIL. ttfb after 0.621 s
PASS; survivors 7/7, no rg port lost; tunnel_promoted x1; no reorder wedge. 12 rows,
hash_ok=10.

## Exit gate
All three sets PASS (compose rss 434128 -> 492068 kB, settled 429708, cpu 42.3 s over
345 s).

Outcome: not merged — the first-byte bound abandoned a chunk that had simply not
started yet, truncating the transfer, and it cost the 100 MB cell. The bound was
withdrawn and the branch re-run as 0290ff96a-smoke (int-F2).
