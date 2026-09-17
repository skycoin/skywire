# 4ddf1e432 — mux sets

The reference sets for this commit are the ones in `../98ff6be85/` (criterion 1,
8 sets, 160 rows, all hash-verified). The only change between the two commits is
#4945 (skysocks-client: `--tunnels N` dials every tunnel through a route group),
client-side, so the references stand. Before row for #4945:
`../98ff6be85/mux-tunnels-2.tsv` — `route_groups=0`, the tunnels had taken the
direct shortcut. Every set here ran through the default `skysocks-client`
instance on :1080 (`APP=skysocks-client`).

The tunnels sets here are the before row for #4946: every tunnel's route group
sits on the same first-hop transport (the direct stcpr to the exit, see
`groups=` in each header), because the candidate-route race ignored the
disjoint-first-hop exclusions. The after row is in `../f385842ab/`.
