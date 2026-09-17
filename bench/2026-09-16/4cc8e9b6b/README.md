# 4cc8e9b6b — mux suite + compose 2x2 after #4970 (ECF/RACK on end-to-end RTT; latency band on windowed min-RTT) + #4971 (exit-open timeouts visible; tunnel sits out), default 4 MiB chunks

Mux sets plus the first composition re-run; the bar is the reference run from `../a8c3b6486/` (same
day, same rig, same `skysocks-client` on :1080 with `--range-port 18080`, default 4 MiB chunks). Legs
sets pin `mux width N`; tunnels sets use N sibling route groups. Exit =
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`, also on 4cc8e9b6b.

Verdicts (`bench/verdict.sh bench/2026-09-16/a8c3b6486 bench/2026-09-16/4cc8e9b6b`), median MB/s
against the best single-route reference of the a8c3b6486 run; campaign14 (1ce356b2d) in parentheses:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 5.51 (via 03f57e7c) | 6.81 (direct stcpr) | 8.05 (via 0371ab4b) | 9.95 (via 0371ab4b) |
| legs-2 | 4.77 (5.40) | 4.01 (3.28) | **8.46** (7.25) | 8.36 (8.98) |
| legs-3 | 1.93 (3.20) | 2.61 (2.12) | 5.31 (6.85) | 7.20 (8.28) |
| tunnels-2 | 3.41 (3.25) | 6.80 (6.00) | 7.25 (8.32) | 2.80 (9.82) |
| tunnels-3 | 3.18 (2.65) | 5.24 (4.62) | 2.95 (7.81) | 8.83 (8.90) |

- **legs-2 50 MB down passes: 8.46 vs the 8.05 bar** (was 7.25), at wire/goodput 1.02 (was 1.11),
  5/5 hashes, a stable 67/33 split and no retransmit storm. This is #4970's intended effect confirmed
  on a multi-leg group: the exit's earliest-completion picker and the RACK threshold now both see the
  ~170 ms end-to-end feedback delay on each leg instead of 10 ms (Amsterdam) vs 95 ms (Atlanta) of
  first-hop RTT, so the slow leg is no longer held back until frames queue and then declared lost.
  10 MB down is 4.77 and the uploads 4.01 / 8.36.
- **tunnels-2 regressed hard on uploads: 50 MB up 2.80 (was 9.82), and it is a standing queue, not
  loss.** All five POSTs went out on the Sydney tunnel (`50cdb857` via
  `0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7`) — `mux-tunnels-2.carrier.tsv`
  rows 16–20 put ~50 MB of `sent_delta` on `50cdb857` and ~0.1–0.6 MB on the direct `b414796d` in
  every one — and the rate decays monotonically across the five: 3.90 → 3.05 → 2.80 → 1.59 →
  0.62 MB/s (12.8 → 80.5 s). The counters say queue: `mux-tunnels-2.recovery.tsv` rg 49160 has
  `retx_sent` 6 → 28 cumulative, `send_window_waits` 0, `wedges` 0, `reorder_pending` 0, while
  `sacks_recv` grows 125 → 476 per row and the last row fires 21 TLP probes; the leg's own latency
  reading in `mux-tunnels-2.legs.json` reaches 13435 ms. Nothing is being dropped — the pipe is just
  filling.
- **Root cause of that regression is in #4970 too.** The same change made the BDP baseline
  (`ecfRttMinMs`) track the combined `max(first-hop, ack-delay)` value. The ack-delay estimator is
  asymmetric and has no time decay, and the per-leg counters persist across rows, so on a 470 ms path
  it ratchets: window → queue → ack delay → higher baseline → larger window, up to the 8 MiB cap.
  Why that tunnel was picked at all: `pickAny` scores rx+tx with *both* directions decayed per busy
  window, so after ~34 s of downloads the direct tunnel's upload capacity had decayed to nothing and
  the choice was decided by download rates (the download split had itself drifted 83/17 → 42/58).
  #4971 never fired here — there were no exit-open timeouts in this set.
- **The same ratchet costs tunnels-2 its 50 MB download cell: 7.25 (was 8.32)**, this time on the
  exit's send window over the Sydney tunnel. Fix PR in flight: keep the BDP baseline on first-hop
  RTT, expire ack-delay estimates when a leg goes idle, and pick uploads on upload capacity.
- **tunnels-3**: 10 MB down 3.18 at 5/5, 50 MB down 2.95 at 4/5 — the single miss is one 15 s stall
  row, which #4971 now logs and counts instead of surfacing only as an http 000. Uploads 5.24 / 8.83.
- **legs-3** is the ratchet again, inside a three-leg group on the Sydney leg: 1.93 (w/g 1.36) /
  2.61 / 5.31 / 7.20, with 31 park/promote events in `mux-legs-3.mux_events.json`.
- Rig note: `rig-restore` left the exit's transport to
  `0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb` missing (5/6 transports) and
  the chain proceeded anyway; it was re-added by hand at 04:52Z before the tunnels-3 set. `deploy.sh`
  now retries and fails the deploy unless the exit reports 6/6.
- Criteria status: wire/goodput is ≤ 1.19 everywhere except legs-3 10 MB (1.36) and compose 10 MB
  (1.43); hashes are 99/100 across the mux sets; every park carries a named reason.

Next: campaign16 re-runs these sets on the ratchet fix — and a smoke test before merge from now on,
since this regression would have shown up in a single 50 MB upload. Then degradation
(`run-degrade.sh`), a dmsg-only reference set, and the standby-pool phase.

## Composition on this commit (`bench/run-compose.sh`, second run)

T2xL2 = tunnels {`03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf` +
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`} ×
{`0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7` +
`0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb`}; dst_ports constant, 5/5
hashes in every cell.

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| T2xL2 | 3.35 (w/g 1.43) | 4.35 (w/g 1.01) | 6.67 (w/g 1.16) | 8.09 (w/g 1.00) |

Better than the first run on 1ce356b2d — 50 MB down 6.67 at w/g 1.16 against 5.63 at 1.47, so the
exit-side retransmit storm inside each tunnel is gone, as #4970 was meant to do — but still under
tunnels-2 alone. Re-measured after the ratchet fix.

## Degradation on this commit (`bench/run-degrade.sh`, 3 trials, 50 MB, cut at 3 s)

| subject | cut | down before→after (MB/s) | ttfb after cut | hash | rebuilt |
|---|---|---|---|---|---|
| legs-2 | `proxy mux rm` leg 2 | 6.2→9.0, 1.8→4.4, 5.3→8.3 | 0.92–0.94 s | 3/3 | **no** |
| legs-2 (up) | same | 6.6→9.1, 7.7→8.6, 7.4→9.5 | 1.69–1.74 s | 3/3 | **no** |
| tunnels-2 | `tp rm` tunnel 2's hop-1 | 1.3→1.1, 2.6→1.4, 1.3→0.7 | 3.4 / 35.5 / 40.4 s | 3/3 | yes, every row |
| tunnels-2 (up) | same | 10.9→8.7, 12.9→7.3, 12.0→9.2 | – | 0/3 (POST truncated, http 100) | yes |

Packet-level mux meets criterion 6: the group's port stays constant, one `group_created` in the whole
set, `leg_removed(operator) → primary_rehomed → reorder_wedge (1.5–1.9 s) → cleared (0.4–0.5 s)`.
Stream-level mux does not: a tunnel is a single-leg group, so losing its transport closes the group
(`group_closed(local close)` → `group_created`); range-split downloads survive on the other tunnel but
take 35–40 s to resume (chunk retry budget), and a POST in flight on the cut tunnel cannot be resumed.
Follow-ups: fail a chunk fast when its tunnel's group closes and refetch on a surviving tunnel; and
composition (two legs per tunnel) so a transport loss is a leg loss, not a group loss.
