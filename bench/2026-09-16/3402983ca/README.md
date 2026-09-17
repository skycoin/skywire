# 3402983ca — mux suite after #4955 and #4956

References: `../98ff6be85/`. Before rows: `../ba69f8547/` (tunnels: every tunnel on the direct
stcpr first hop, the completed trail showing the transport-creation hook race taking
`directRoute` past the exclusions → #4955; legs-3: the exit re-sent 10,919 frames for 20,144
sent, the per-leg in-flight never bounded → #4956, a per-leg send window from SACK-proven
delivery with a parked writer). Legs sets pin `mux width N`. Sets ran through the default
`skysocks-client` instance on :1080.
