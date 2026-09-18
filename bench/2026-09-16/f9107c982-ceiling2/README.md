# f9107c982-ceiling2 — the downlink ceiling over the N best distinct routes (chain Z)

Binary: f9107c982 (the chain X deploy reused; bench from develop 2a38b1130 = #5016,
"the downlink ceiling is measured over the N best distinct routes, not the direct path
alone"). This is the re-measurement chain V's result demanded.
Ran: 2026-09-18 04:28Z–04:31Z, chain Z (`scratchpad/chainZ.log`).
Method: 3 concurrent clients, 50 MB each, 2 trials; uplink over 3 direct clients, downlink
over the 3 best distinct routes of the paired ranking, plus one `uplink-via` row.

## ceiling.tsv
```
# kind	clients	trial	bytes	sum_MBps	route:rate_MBps	hashes_ok/n
uplink	1	1	50000000	9.10	direct:9.10	1/1
uplink	3	1	50000000	11.25	direct:3.84,direct:3.70,direct:3.71	3/3
uplink	1	2	50000000	10.60	direct:10.60	1/1
uplink	3	2	50000000	11.21	direct:3.75,direct:3.70,direct:3.76	3/3
downlink	1	1	50000000	3.22	via-0371ab4b:3.22	1/1
downlink	3	1	50000000	9.26	via-0371ab4b:3.44,via-03f57e7c:2.15,via-0281a102:3.67	3/3
downlink	1	2	50000000	5.80	via-0371ab4b:5.80	1/1
downlink	3	2	50000000	9.85	via-0371ab4b:3.76,via-03f57e7c:2.85,via-0281a102:3.24	3/3
uplink-via	1	1	50000000	10.33	via-0371ab4b:10.33	1/1
uplink-via	1	2	50000000	10.28	via-0371ab4b:10.28	1/1
# ceiling uplink: single 9.85, 3 concurrent 11.23 MB/s, grew=yes (x1.14)
# ceiling downlink: single 4.51, 3 concurrent 9.55 MB/s, grew=yes (x2.12)
# uplink over via-0371ab4b alone: 10.30 MB/s vs 9.85 MB/s direct — the direct uplink IS a bottleneck
```
10/10 transfers hash-verified.

Decided: the endpoint ceilings the rest of the day is scored against — **uplink 11.23,
downlink 9.55 MB/s**. Measured over distinct routes the downlink grows x2.12 rather than
chain V's x1.07, which is what makes criterion 4's "0.95 x ceiling" bound meaningful; this
`ceiling.tsv` was copied into the later chains' result dirs.
