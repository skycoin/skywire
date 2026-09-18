# 845dd383e-ceiling — first endpoint ceiling measurement (chain V)

Binary: 845dd383e (the chain T deploy reused; bench scripts from develop eb6c60130 =
`bench/run-ceiling.sh`, #5011). No mux sets — this dir holds one `ceiling.tsv`: what the
endpoint pair can carry at all, so criteria 2 and 4 can be scored against a ceiling
instead of against a single reference.
Ran: 2026-09-18 03:05Z–03:08Z, chain V (`scratchpad/chainV.log`).
Method: 3 concurrent **direct** clients, 50 MB each, 2 trials, each transfer sink-verified.

## ceiling.tsv
```
# kind	clients	trial	bytes	sum_MBps	rates_MBps	hashes_ok/n
uplink	1	1	50000000	9.03	9.03	1/1
uplink	3	1	50000000	10.56	3.52,3.51,3.53	3/3
downlink	1	1	50000000	4.56	4.56	1/1
downlink	3	1	50000000	5.27	1.74,1.78,1.75	3/3
uplink	1	2	50000000	9.04	9.04	1/1
uplink	3	2	50000000	10.83	3.61,3.63,3.59	3/3
downlink	1	2	50000000	4.89	4.89	1/1
downlink	3	2	50000000	4.81	1.63,1.59,1.59	3/3
# ceiling uplink: single 9.04 MB/s, 3 concurrent 10.70 MB/s, grew=yes (x1.18)
# ceiling downlink: single 4.72 MB/s, 3 concurrent 5.04 MB/s, grew=yes (x1.07)
```
8/8 transfers hash-verified.

Decided: the uplink ceiling is real (x1.18 over a single client) but the **downlink**
measured this way is not a ceiling at all — three clients over the one direct path grew
only x1.07, because the direct path is itself the bottleneck. That finding is what #5016
fixed, and chain Z re-measured the downlink over the three best distinct routes.
