# fe53f42dc-smoke — int-D (audit fixes, first pass)

Commit under test: fe53f42dc, merge of fix/router-audit-receive-teardown into int-D —
the audit branches later opened as #4996 (tunnel lifecycle leaks, unchecked app
mux-event ingress) and #4998 (a dropped reorder packet is never SACKed, bounded SACK
set, Close stops holding rg.mu for 4 s), on top of #4992.
Ran: 2026-09-17 20:56Z-21:15Z, chain D (scratchpad/chainD.log).
Sets: mux-legs-2, mux-tunnels-2, mux-standby-8.

## Verdict rows (bar mode)
    mux-legs-2     down 10  5.20 vs 5.00 PASS   up 10  4.83 vs 7.52 FAIL
    mux-legs-2     down 50  3.88 vs 5.43 FAIL   up 50  8.95 vs 10.32 FAIL
    mux-tunnels-2  down 50  8.87 vs 5.43 PASS   up 50  9.03 vs 10.32 FAIL
    mux-standby-8  down 10  2.40 vs 5.00 FAIL   up 10  7.05 vs 7.52 FAIL (hash 2/3)
    mux-standby-8  down 50  2.37 vs 5.43 FAIL   up 50  0.47 vs 10.32 FAIL (w/g 0.02)
The standby uploads collapsed: 0.47 MB/s at 50 MB with wire/good 0.02, and one of the
three 10 MB uploads failed its hash.

## Cut row (mux-standby-8)
Pool settled at 8 groups (2 active, 6 standby). Cut tp 95839ad0-b588-0b1d-8475-
45fba6ae993c on rg 49172 at 5.220 s after 790528 bytes; ttfb after 0.621 s — PASS.
hashes 3/3; survivors 7/7, rg 49172 lost (only the cut group); tunnel_promoted x1; no
reorder wedge. 12 rows, hash_ok=11.

## Exit gate
mux-tunnels-2 PASS, mux-legs-2 PASS.

Outcome: not merged — the standby upload collapse had to be understood first. The
same audit work re-ran as deac6fa5a-smoke (int-D2) and merged from there.
