# 1647ae058-exitwin — exit-side `--ecf-min-window` confirming experiment (chain U)

Binary: 1647ae058 (the chain S deploy reused; no redeploy). The experiment changes one
**exit-side** router setting live and runs the composition set against it, then restores it:
`--ecf-min-window 8KiB` (`min8k/`) vs the shipped 128 KiB default (`min128k/`, the control).
Ran: 2026-09-18 02:41Z–02:52Z, chain U (`scratchpad/chainU.log`).
Set: mux-compose-T2xL2, 2x2, 50 MB and 100 MB down, 2 trials each.

## Verdict rows
```
== min8k
mux-compose-T2xL2        down  50     2/2    5.96      5.13      6.79      2/2      1.32
mux-compose-T2xL2        down  100    2/2    7.53      7.29      7.77      2/2      1.00
== min128k
mux-compose-T2xL2        down  50     2/2    5.68      5.40      5.97      2/2      1.44
mux-compose-T2xL2        down  100    2/2    7.78      6.92      8.64      2/2      1.09
```
(columns: set, dir, MB, ok/n, median, min, max, hashes, w/g.) Paired ratios are empty —
`PAIRED_REF=auto` resolved to `direct` here, so only the two arms compare to each other.
Exit retransmit counters over the run: min8k `retx_sent 167 -> 1372` (`retx_req_sack`
141 -> 1163); min128k `retx_sent 38 -> 3250` (`retx_req_sack` 22 -> 2809).
EXITRES PASS in both arms.

Decided: an 8 KiB exit minimum window does not lift composition — the two arms are within
the run-to-run spread — so the composition cap is not the exit's minimum-window floor and
the default 128 KiB stays.
