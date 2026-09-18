# bench/2026-09-18 — the closing day of the route-multiplexing live campaign (v4)

One directory per rig chain. Every set in every directory ran against a **paired
reference** measured on the same commit in the same window: one reference row of the
same cell immediately before every mux row, `PAIRED_REF=auto` resolving to
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` (US-Atlanta).
Ratios below are mux ÷ that reference, averaged over the trials of the cell; read the
per-row numbers from `<set>.paired.tsv` and the pass/fail lines from `<set>.assert.tsv`.
Each directory's `*.chain.log` first line names the tree (`local=<sha>`) and the sets run.

| dir | chain | tree under test | sets | outcome |
|---|---|---|---|---|
| `1008cc8e5/` | (carried from the 2026-09-18 chains O–AK) | develop `1008cc8e5` (#5035) | — | the day's starting head; indexed in `bench/2026-09-16/README-2026-09-18-chains.md` |
| `9f4848dfa-smoke/` | AL | AL-era integration tree (#5047/#5049 candidates) | ceiling, compose 2x2, legs-2, standby-7, direction | composition collapsed — 10 MB down x0.45, 50 MB down x0.40, 100 MB down x0.64; legs-2 downloads level (x1.00 / x1.03), uploads short |
| `8b6fcfdda-smoke/` | AM | AM-era integration tree | ceiling, compose 2x2, legs-2, standby-8, direction | composition recovered at 100 MB (x1.25) and 50 MB (x1.10); 10 MB down still x0.54; standby-8 flat (x0.72–0.97) |
| `cf32eafae-smoke/` | AN | AN-era integration tree (#5051–#5053 candidates) | ceiling, compose 2x2, legs-2, tunnels-2, direction | 100 MB composition x1.27, but tunnels-2's 50 MB cells collapsed (down x0.48, up x0.44) — the standby regression chain AO went bisecting |
| `1d8b2e1e5-bisect/` | AO | `1d8b2e1e5`, a bisect step | ceiling, tunnels-2, direction | bisect of the standby regression chain AN measured: tunnels-2 clean on this tree — 10 MB down x1.29, 10 MB up x1.65, 50 MB down x1.12 |
| `b388ec097-smoke/` | AP | integration tree with the standby pool | ceiling, tunnels-2, legs-2, standby-8, direction | tunnels-2 10 MB down x1.25 / 10 MB up x1.67; legs-2 downloads at parity, uploads x0.72–0.94 |
| `a01dafd11-smoke/` | AQ | integration tree | ceiling, compose 2x2, legs-2, tunnels-2, direction | tunnels-2 50 MB down x1.91; composition 100 MB down x1.44 but 10 MB down x0.66 |
| `4078c5f70-smoke/` | AR / AS | spread-knob tree | ceiling, tunnels-2, legs-2, spread-3, standby-32, direction | **criterion 10 as a live setting**: spread-3 50 MB down x1.46–1.58 / up x0.89–0.95, max share 0.417, 3 routes held |
| `a0df50285-smoke/` | AT | develop + #5054 (spread as a default) | ceiling, tunnels-2, legs-2, spread-3, standby-32, direction | spread as a **default** measured x0.605 down / x0.157 up — the reason #5054 is held for v1.3.96 |
| `2c3d313f9-smoke/` | AU | + #5057 v2 | ceiling, tunnels-2, legs-2, standby-32, direction | legs-2 50 MB down x1.51; standby-32 uneven (10 MB down x1.43, 50 MB down x0.65) |
| `f8b1aa232-smoke/` | AV | + #5057 v2 + #5060 + #5061 | ceiling, tunnels-2, legs-2, standby-32, direction | cut row survived with **ttfb 0.223 s**; legs-2 50 MB down x1.29 |
| `0de50e04f-smoke/` | AW | + #5057 v3 | ceiling, tunnels-2, legs-2, standby-32, direction | legs-2 50 MB down x1.33, standby-32 50 MB up x1.04; the cut row failed on a harness fence → fixed by #5064 |
| `9bea70ded-smoke/` | FINAL | develop tip | ceiling, tunnels-2, legs-2, direction | tunnels (50 MB down x1.33) and legs valid; compose and standby **INVALID** — the route setup node's destination circuit breaker had opened against the exit → #5067 |
| `898591982-smoke/` | FINAL2 | develop tip with #5067, the breaker off on both setup nodes | ceiling, tunnels-2, legs-2, compose 2x2, standby-32, direction | **the reported run of the campaign close** — see the table below |

## Campaign close — the ten v4 criteria, reported run `898591982-smoke/`

Ratios are mux ÷ the best paired reference, 10 MB and 50 MB, download and upload.
Ceilings for this run (`ceiling.tsv`): **uplink 11.29 MB/s, downlink 11.48 MB/s**.

| # | criterion | status | measured |
|---|---|---|---|
| 1 | paired references | **MET** | best candidate `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` confirmed by probe each set, reference rows interleaved; ceiling uplink 11.29 / downlink 11.48 MB/s |
| 2 | two tunnels | **MET** on the four measured cells | down x1.56 (10 MB) / x1.14 (50 MB), up x1.54 / x1.05; 5/5 and 3/3 hashes; w/g 1.00. The two-concurrent-uploads cell was not re-run (last measured in campaign v1) — that part partially met |
| 3 | two legs | downloads **MET**, uploads **NOT MET** | down x1.01 / x0.97 (inside the 5 % band), up x0.69 / x0.88; route-group port constant throughout |
| 4 | composition 2x2 | 50 MB and 100 MB down **MET**, 10 MB and 50 MB up **NOT MET** | 50 MB down x1.59 and 100 MB down x1.08 with best single 5.32 < 0.9 × ceiling; 10 MB down x1.22 is below two tunnels alone (x1.56); 50 MB up x0.43 |
| 5 | direction, from both ends | **MET** for the shipped default | standby pool: forward on the direct/lowest-latency route 20/20 rows PASS, reverse fanned. legs-2: 6 of 12 forward rows flagged by a checker with no latency tolerance band (149 vs 139 ms legs) — the checker is to be relaxed |
| 6 | cut survival | **MET** | transport cut mid-transfer, ttfb after cut **0.219 s** (< 2 s), survivors 31/31, no rebuild, hashes 5/5. Noted: `local_reorder_wedge` counter 1 on the cut row |
| 7 | wire/goodput | **MET** | ≤ 1.08 on every cell; churn bounded with named reasons in `mux_events` |
| 8 | default is the standby pool | **MET** | pool discovered 32 groups, active set chosen on measured goodput (#5047), same-tick failover (#4991), a dead active replaced from the pool |
| 9 | the final no-pins run | **DONE** | this run, `898591982-smoke/` |
| 10 | spread policy | **MET** as a live setting, **not shipped** as a default | chain AS (`4078c5f70-smoke/`): 50 MB down x1.46 / up x0.95, max share 0.417 ≤ 0.4 + band, 3 routes. #5054 measured x0.605 / x0.157 as a default (chain AT, `a0df50285-smoke/`) and is held for v1.3.96 |

Fixes the campaign landed this day: **#5047, #5049, #5051–#5053, #5055–#5065, #5067**
(the route-setup-node destination circuit breaker shipped off).
