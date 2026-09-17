# ba69f8547 — mux suite after #4952, #4953 and #4954

References: `../98ff6be85/`. Before rows: `../b65726e64/` (tunnels: every tunnel on the direct
stcpr first hop with the trail ending at the seeded exclusion → #4952; legs-2: the second
pinned leg still parked → #4953; legs-3: 26,801 of the exit's 29,360 retransmits from the
reactive SACK path → #4954, per-leg loss thresholds). Legs sets pin `mux width N`. Sets ran
through the default `skysocks-client` instance on :1080.
