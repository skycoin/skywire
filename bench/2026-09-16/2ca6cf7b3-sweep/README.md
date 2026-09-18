# 2ca6cf7b3-sweep — live knob sweeps on the chain O deploy (chains P, Q, R)

Binary: 2ca6cf7b3 (chain O deploy, both ends); the local checkout moved forward for the
bench scripts only (4d3e517d1 = + #5006, then #5007). No redeploy — every value is a
live `proxy settings` / `route settings` change on the running app.
Ran: 2026-09-18 01:24Z–02:22Z. `sweep/` = chains P and Q (`scratchpad/chainP.log`,
`chainQ.log`); `route/` = chain R (`scratchpad/chainR.log`).

## sweep/ — upload knobs (run-mux.sh, mux-tunnels-2) and composition knobs (run-compose.sh)
```
value	mux-tunnels-2/10down	mux-tunnels-2/10up	mux-tunnels-2/50down	mux-tunnels-2/50up
1MiB	4.83;-;2/2;1.04	5.11;-;2/2;1.00	7.59;-;2/2;1.02	8.69;-;2/2;1.00
2MiB	4.65;-;2/2;1.04	6.47;-;2/2;1.00	6.85;-;2/2;1.03	8.80;-;2/2;1.00
4MiB	2.99;-;2/2;1.02	5.25;-;2/2;1.01	5.65;-;2/2;1.03	9.84;-;2/2;1.00
value	mux-tunnels-2/10down	mux-tunnels-2/10up	mux-tunnels-2/50down	mux-tunnels-2/50up
2	4.40;-;2/2;1.03	5.02;-;2/2;1.00	5.96;-;2/2;1.08	9.84;-;2/2;1.00
8	3.94;-;2/2;1.14	5.38;-;2/2;1.00	6.64;-;2/2;1.00	8.87;-;2/2;1.00
value	mux-compose-T2xL2/50down	mux-compose-T2xL2/100down
2MiB	5.22;-;2/2;1.19	5.85;-;2/2;1.24
8MiB	6.14;-;2/2;1.15	5.71;-;2/2;1.12
4	5.27;-;2/2;1.17	7.41;-;2/2;1.08
16	4.05;-;2/2;1.47	5.55;-;2/2;1.02
1	-	-
4	4.80;-;2/2;1.34	6.47;-;2/2;1.01
```
(rows: `upload.chunk_bytes`, `upload.concurrency`, then `chunk.max_bytes`,
`chunk.concurrency`, `chunk.per_tunnel`.) Every paired ratio is empty — `PAIRED_REF=auto`
resolved to `direct` in each value directory, so these are unpaired medians only.

## route/ — chain R, exit/local `route settings` on mux-legs-2, paired vs 0371ab4b
`--ecf-max-window` 4MiB: 10down x0.207/x0.867, 50down x0.948/x0.899, 50up x0.688/x2.070.
16MiB: 10down x0.248/x0.675, 50down x1.290/x0.759 (w/g 1.39 on the 10 MB cell).
`--ecf-window-margin` 1.5: 50down x0.815/x0.708; 3.0: 50down x0.381/x0.712, w/g 1.21.
Settings restored to the shipped 8 MiB / margin 2 after each block.

Decided: no upload or composition knob value beats the defaults by more than the run-to-run
spread, and both ECF window changes are worse than the 8 MiB default — the 10 MB upload gap
and the composition cap are not a tuning problem.
