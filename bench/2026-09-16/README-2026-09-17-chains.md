# Chain runs of 2026-09-17 (UTC)

Sixteen deploy-and-measure chains on the frozen rig, from the standby-pool baselines
through the audit, striped-upload and chunked-composition branches. Each result dir
carries its own README with the verdict rows, the cut row and the exit gate; this is
the index. Times are UTC — chains M and N cross into 2026-09-18. The paired reference
is 0371ab4b throughout; bars come from the 67dddb8be reference sweep.

| Chain | Dir / commit | PRs | Key numbers | Outcome |
|---|---|---|---|---|
| 21 | `67dddb8be` | #4989 (+#4985/86/87/88) | legs-2 paired x1.110/x0.978/x0.991; tunnels-2 up 50 **0.50 MB/s x0.055**; up2 sum ratio 0.482/0.484; standby-0 INVALID (pool never grew) | shipped as f453df391; kept as the day's paired-reference baseline |
| A | `dc23fd9ff-smoke` | #4991 | all 12 bar rows FAIL (bar an hour stale); cut ttfb 1.180 s PASS, pool 8, hashes 3/3 | merged as 385138588 |
| B | `120e1c31c-smoke` | #4992 | tunnels-2 down 50 8.16 PASS; standby-8 down 50 5.78 PASS; cut ttfb **3.724 s FAIL** | superseded by 2e6d92b18-smoke |
| B2 | `2e6d92b18-smoke` | #4992 | cut ttfb **1.207 s PASS** (first-tick retirement); pool 7; exit gate PASS both sets | merged as 128bdd0eb |
| C | `027933c35-smoke` | none (compose v1) | compose paired 10down x0.623, 10up x0.401, 50down x1.375, 100down x0.934; cut ttfb **19.336 s FAIL** | not merged — 10 MB cells regressed |
| D | `fe53f42dc-smoke` | #4996 + #4998 (int-D) | standby-8 up 50 **0.47 MB/s, w/g 0.02**, up 10 hash 2/3; cut ttfb 0.621 s PASS | not merged — standby uploads collapsed |
| E | `3194b7cc8-smoke` | #4997 + #4999 (int-E) | degrade t1 **http=502**, 46985655/50000000 B, hash 0; t2 200, ttfb 3.212 s; up2 INVALID | not merged — 502 on a mid-upload cut |
| F | `e0d9e0430-smoke` | none (compose v2) | compose paired 10down x1.479, 50down x1.329, 100down x1.076 (all PASS); cut ttfb **19.741 s FAIL** | not merged — ttfb unchanged |
| G | `deac6fa5a-smoke` | #4996, #4998 (int-D2) | standby-8 up 50 8.60 (w/g 1.00, 3/3); cut ttfb 0.626 s PASS, all asserts PASS | merged — #4996 a8fe8d489, #4998 872ebfe5e |
| H | `d6fecd1c3-smoke` | #4997, #4999 (int-E2) | degrade 2/2 rows http=200, hash 2/2, ttfb 1.748/1.778 s; 6 of 8 paired cells < 0.80 | not merged — degrade fixed, throughput low |
| I | `91490ed9a-smoke` | none (compose v3) | compose 10down 0.94 (2/3 hashes), 50down 1.16 (2/3), 100down 2.19; cut ttfb **27.143 s FAIL** | dropped — 2 MiB head-chunk cap was a regression |
| J | `a6a42506f-smoke` | #4997, #4999 (int-E3) | per-row ratios 0.110–7.923 (route lottery); up2 ratio 0.562/0.566; exit gate **FAIL rss +94 MiB** | not merged — gate failed, rows unreadable |
| K | `c302e1746-smoke` | none (compose v4) | pool settled at **3**; cut ttfb unmeasurable (no byte after the cut) FAIL; compose rows 0.068–2.025 | not merged — pool-3 snapshot only |
| L | `0186db249-smoke` | #4997, #4999, #5001 (int-E4) | tunnels-2 50up x1.004 PASS, 50down x1.440 PASS; up2 ratio 0.829/0.806; degrade 2/2 hashes, ttfb 1.753/1.894 s | merged — #4997 5bd77c293, #4999 e92e9bdf9, #5001 07748399b |
| M | `27bd7a0d2-smoke` | none (int-F) | standby cut returned 338902/50000000 B, hashes after cut **2/3 FAIL**; 100down rows 0.791/0.428/0.586 | not merged — chunk first-byte bound withdrawn |
| N | `0290ff96a-smoke` | #4995 + #5004 (int-F2) | smoke 4/8 bar rows PASS (tunnels-2 50up 10.17, 50down 8.79); cut ttfb 0.682 s, every assert PASS | merged — #4995 ff081323f, #5004 6168f189c |

Of the twelve branches carried through chains C-N, four merged (G, L, N and the
int-D/int-E work they carried); the composition line (C, F, I, K) and the withdrawn
first-byte bound (M) did not.
