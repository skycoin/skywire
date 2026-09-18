# 2e6d92b18-smoke — #4992 promoter smoke (final tip)

Commit under test: 2e6d92b18 "fix(skysocks): a dead tunnel is retired by the first
tick that sees it, not by the 15s one" — the final tip of the #4992 branch (promoter
and idle audition) on top of #4991/#4994.
Ran: 2026-09-17 19:48Z-20:06Z, chain B2 (scratchpad/chainB2.log). The rig needed one
retry to reach 6/6 hop-1 stcpr before the run started.
Sets: mux-legs-2, mux-tunnels-2, mux-standby-7.

## Verdict rows (bar mode)
    mux-legs-2     down 10  4.96 vs 5.00 FAIL (w/g 1.21)   up 10  2.59 vs 7.52 FAIL
    mux-legs-2     down 50  4.30 vs 5.43 FAIL (w/g 1.25)   up 50  8.59 vs 10.32 FAIL
    mux-tunnels-2  down 10  4.64 vs 5.00 FAIL              up 10  6.51 vs 7.52 FAIL
    mux-tunnels-2  down 50  4.60 vs 5.43 FAIL              up 50  9.00 vs 10.32 FAIL
    mux-standby-7  down 50  2.67 vs 5.43 FAIL              up 50  8.98 vs 10.32 FAIL

## Cut row (mux-standby-7)
Pool settled at 7 groups (quiet 20 s). Cut tp f1012467-de9d-09a9-8b49-62a43945c24b on
rg 49173 at 5.000 s after 4194304 bytes; ttfb after 1.207 s — PASS (want < 2), down
from 3.724 s on 120e1c31c-smoke, which is what the first-tick retirement fix bought.
hashes 3/3; survivors 7/7, no rg port lost; tunnel_promoted x1; no reorder wedge.
12 rows, hash_ok=12.

## Exit gate
mux-tunnels-2 PASS (rss -17 MiB, settled 388236 kB); mux-legs-2 PASS.

Outcome: the branch merged as 128bdd0eb (#4992). Together with dc23fd9ff-smoke and
120e1c31c-smoke this closes the standby-pool/promoter work that the C-N chains build
on.
