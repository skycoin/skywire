# 9cd49cbea — mux suite after #4947 (dial_decision trail) and #4948 (DSACK ack-delay)

References: `../98ff6be85/` (criterion 1). Before rows: tunnels sets and legs sets in
`../f385842ab/` (legs-3 there: 37207 retransmits for 20304 frames on the exit). Between
f385842ab and this commit: #4947 (diagnostic only) and #4948 (the storm fix). Legs sets
here run with `proxy mux width N` pinned so every pinned leg stripes from the first row;
sets ran through the default `skysocks-client` instance on :1080.
