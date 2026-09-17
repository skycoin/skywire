# 45905e2f5 — mux suite with a range-capable sink split across tunnels (after #4957, #4958, #4959)

References: `../98ff6be85/` (sink on :18080, one connection per transfer). Before rows:
`../3402983ca/` — tunnels with disjoint first hops at last (#4955) but every transfer on one
tunnel (no Range at the sink → #4957 makes the sink range-capable, and these sets pass `--range-port 18080` so the proxy splits a download from the
:18080 sink across tunnels — Caddy owns the exit's :80); legs sets with the
first send window (#4956), where the exit (capacity mode) never parked and our upload was
pinned at 1.68 MB/s by a window doubling every ~5 s → #4958 (every mode, 250 ms refresh).
Legs sets pin `mux width N`. Sets ran through the default `skysocks-client` on :1080.
