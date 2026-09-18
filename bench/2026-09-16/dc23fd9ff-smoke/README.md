# dc23fd9ff-smoke — #4991 pre-merge smoke

Commit under test: dc23fd9ff "changelog: the carrier-class ranking (#4991)" — the tip
of the #4991 branch (same-tick standby failover, tunnel switch events, carrier-class
ranking for diversify dials).
Ran: 2026-09-17 19:06Z-19:24Z, chain A (scratchpad/chainA.log).
Sets: mux-legs-2, mux-tunnels-2, mux-standby-8 (+ drift.tsv).

## Verdict rows (bar mode, no contemporaneous reference)
    mux-legs-2     down 10  3.16 vs 5.00 FAIL (w/g 1.59)   up 10  4.64 vs 7.52 FAIL
    mux-legs-2     down 50  4.56 vs 5.43 FAIL (w/g 1.53)   up 50  8.63 vs 10.32 FAIL
    mux-tunnels-2  down 10  1.79 vs 5.00 FAIL              up 10  4.55 vs 7.52 FAIL
    mux-tunnels-2  down 50  3.25 vs 5.43 FAIL              up 50  8.30 vs 10.32 FAIL
    mux-standby-8  down 50  3.81 vs 5.43 FAIL              up 50  8.47 vs 10.32 FAIL
Every row FAIL against the bar, but the bar is the 67dddb8be run taken an hour earlier
and the references swing ~2x an hour, so these rows carry little weight on their own.

## Cut row (mux-standby-8)
Pool settled at 8 groups (quiet 20 s). Cut tp 95839ad0-b588-0b1d-8475-45fba6ae993c on
rg 49176 at 5.079 s after 4194304 bytes before the cut; ttfb after 1.180 s (PASS,
want < 2); hashes 3/3; survivors 7/7, no rg port lost; tunnel_promoted x1. 12 rows,
hash_ok=12.

## Exit gate
mux-tunnels-2 FAIL (rss +75 MiB > 64 MiB); mux-legs-2 PASS.

Outcome: the branch merged as 385138588 (#4991). The run itself is superseded as a
same-tick-failover measurement by 120e1c31c-smoke and 2e6d92b18-smoke.
