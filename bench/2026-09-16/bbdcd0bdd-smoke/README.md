# bbdcd0bdd-smoke — the standby tunnel pool (#4986), before/after rows

Smoke of the branch tip that merged as 9220c29cf: `--tunnels 2` with the standby pool on by default.
Both ends on bbdcd0bdd, exit `022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`,
two download trials and three upload trials per cell, unpaired (`PAIRED=0`) so the rows compare
directly with the earlier smokes, exit resource gate on.

The pool filled to all seven rig routes on start (two active, five standby: Mumbai, Sydney, Atlanta,
Singapore, direct stcpr, Frankfurt, Amsterdam). Standby tunnels carried only their 5 s RTT probes.

| set | 10 down | 10 up | 50 down | 50 up | hashes |
|---|---|---|---|---|---|
| tunnels-2 (pool 7) | 5.11 | 6.67 | 7.40 | 10.13 | 10/10 |
| legs-2 | 5.14 | 4.75 | 7.96 | 6.38 | 10/10 |
| campaign20 tunnels-2 (no pool, a5b973a97) | 4.54 | 7.25 | 7.37 | 9.07 | 20/20 |
| campaign20 legs-2 | 5.33 | 4.64 | 7.05 | 8.81 | 20/20 |

Exit gate: tunnels-2 RssAnon 306 → 361 MB (+54 MiB, allowance 64), 0.08 core (allowance 0.55), PASS;
legs-2 +13 MiB, 0.09 core, PASS. Holding eight groups idle for 90 s earlier the same evening cost the
exit nothing measurable (RssAnon flat within 12 MB, 0.06 core).

`../18ad5f897-smoke/` is the first build of the same branch: its tunnels-2 set came up with the
router's primary on Mumbai and the pool parked at five groups after three failed dials, which
bbdcd0bdd fixed (a failed dial is retried in bounded rounds, not treated as settlement).
`../6745065a5-degrade/` is the failover before-row taken on the pool-less default: downloads survive a
first-hop cut in 0.6–1.2 s with the range-split rescue, one of three groups rebuilt; uploads die with
their tunnel three of three.
