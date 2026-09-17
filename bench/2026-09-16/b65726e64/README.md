# b65726e64 — mux suite after #4949, #4950 and #4951 (retransmit source counters)

References: `../98ff6be85/`. Before rows: `../9cd49cbea/` (tunnels sets: every tunnel on the
direct stcpr first hop, the `dial_decision` trail showing the RSN-oracle path ignoring the
exclusion, fixed by #4949; legs-2: the second pinned leg parked with zero bytes for all rows,
fixed by #4950, which also records `leg_promoted` events). Legs sets pin `mux width N`.
Sets ran through the default `skysocks-client` instance on :1080.
