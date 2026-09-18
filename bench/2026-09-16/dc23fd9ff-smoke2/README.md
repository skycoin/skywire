# dc23fd9ff-smoke2 — hand re-run of the legs-2 set on the chain A binary

Commit under test: dc23fd9ff (#4991, failover + events), the binary chain A deployed on
2026-09-17; `bench/2026-09-16/dc23fd9ff-smoke/` is that chain's own result dir. This
directory is a **hand re-run** of `mux-legs-2` alone, made 2026-09-17 14:28 local to
re-read the set after the chain A bars came back an hour stale. There is no chain log.
Set: mux-legs-2 only, 2 legs pinned to 03f57e7c and 0371ab4b on rg 49189
(`stcpr>03f57e7c@fdab37dd;stcpr>0371ab4b@95839ad0`), one active group, no standby.

## Rows (mux-legs-2.tsv, 10 rows, hash_ok=10)
```
mux-legs-2-t1	down	10000000	2339821	200	10000000	4.273835	1
mux-legs-2-t2	down	10000000	3661512	200	10000000	2.731118	1
mux-legs-2-t1	up	10000000	4657618	200	10000000	2.147024	1
mux-legs-2-t2	up	10000000	4654977	200	10000000	2.148242	1
mux-legs-2-t3	up	10000000	6208554	200	10000000	1.610688	1
mux-legs-2-t1	down	50000000	4020234	200	50000000	12.437088	1
mux-legs-2-t2	down	50000000	4055211	200	50000000	12.329816	1
mux-legs-2-t1	up	50000000	8438697	200	50000000	5.925091	1
mux-legs-2-t2	up	50000000	8247395	200	50000000	6.062524	1
mux-legs-2-t3	up	50000000	8146620	200	50000000	6.137518	1
```
(columns: name, dir, size, goodput B/s, http, got, seconds, hash_ok.) No verdict table was
produced — the run was scored by hand against the chain A references.

Decided: nothing on its own; it is kept as the raw counter set (`.recovery.tsv`,
`.exit-recovery.tsv`, `.legs.json`, `.mux_events.json`) behind the chain A legs-2 reading,
all 10 rows hash-clean at 4.0 MB/s down and 8.2–8.4 MB/s up on 50 MB.
