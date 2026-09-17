# a5b973a97 — campaign20, the closing run: #4981 (two tunnels by default, sibling dialed on the best-ranked unused route), #4982 (sink hashes an object once, optimistic CONNECT), #4983 (a sibling route is ranked by its full path latency, LAN neighbours are not diversity)

All four mux sets plus both compositions on the frozen rig, both ends at a5b973a97, exit
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1`. The bars are a **fresh
reference set taken into this same directory** (`ref-*.tsv`) immediately after the mux sets, same
`skysocks-client` on :1080 with `--range-port 18080`, default 4 MiB chunks — so the verdict is
`bench/verdict.sh bench/2026-09-16/a5b973a97 bench/2026-09-16/a5b973a97`.

The two tunnels sets were **auto-dialed by the shipping default** (`skyenv.SkysocksClientTunnels = 2`,
`cmd/skywire-cli/commands/proxy/tunnels_default_test.go:19`) with no pins at all. Only the legs and
compose sets were pinned.

## Why the bars were re-measured

The drift probe (`drift.tsv`, 2 trials, 50 MB, against the `../6a86be622/` references) split again:

| probe | now | ref | ratio |
|---|---|---|---|
| direct stcpr down 50 | 2.11 | 5.11 | 0.41 |
| direct stcpr up 50 | 9.52 | 9.96 | 0.96 |
| via `0371ab4b…` down 50 | 10.06 | 6.09 | 1.65 |
| via `0371ab4b…` up 50 | 9.72 | 9.30 | 1.05 |

Two cells past 25 %, in opposite directions — the direct downlink had collapsed to 41 % of its own
reference of a few hours earlier while Atlanta had gained 65 %. So the whole eight-set reference
suite was re-measured after the mux sets and the verdict uses it.

## Contemporary bars (fresh `ref-*` medians, MB/s)

| reference | 10 down | 10 up | 50 down | 50 up |
|---|---|---|---|---|
| direct stcpr (`b414796d`) | **5.00** | **7.52** | 4.32 | **10.32** |
| direct squicr | 1.03 | 1.76 | 0.55 | 1.89 |
| via `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` (Atlanta) | 4.29 | 7.22 | **5.43** | 10.26 |
| via `03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf` (Amsterdam) | 3.26 | 5.12 | 5.01 | 9.68 |
| via `0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb` (Frankfurt) | 1.77 | 6.45 | 4.86 | 9.70 |
| via `02c483938539bd7820f72e48ed6056bab68e221e1108d23965a2903221495e4af7` (Singapore) | 4.38 | 3.29 | 3.86 | 6.05 |
| via `02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd` (Mumbai) | 3.39 | 4.15 | 4.59 | 7.03 |
| via `0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7` (Sydney) | 2.45 | 1.65 | 3.53 | 1.19 |

Bars in bold: **10 down 5.00, 10 up 7.52, 50 up 10.32 direct stcpr; 50 down 5.43 via Atlanta.** All
160 reference rows hash-verified, wire/goodput 1.00 in every cell. The 50 MB download bar was 8.84
in `../8170476dd/` the day before and 6.63 in `../6a86be622/` that morning — the references swing by
2x inside an hour, which is the single largest source of noise in this campaign.

## Per-set medians (`bench/summarize.sh`, MB/s)

| set | 10 down | 10 up | 50 down | 50 up | hashes | w/g worst |
|---|---|---|---|---|---|---|
| tunnels-2 (auto) | 4.54 | 7.25 | 7.37 | 9.07 | 20/20 | 1.01 |
| tunnels-3 (auto) | 4.71 | 5.57 | 7.00 | 9.11 | 20/20 | 1.01 |
| legs-2 | 5.33 | 4.64 | 7.05 | 8.81 | 20/20 | 1.06 |
| legs-3 | 5.14 | 5.09 | 6.05 | 8.67 | 20/20 | 1.36 |
| compose-T2xL2 (2x2) | 3.47 | 4.53 | 6.51 | 8.94 | 20/20 | 1.10 |
| compose-T2xL1 (2x1) | 4.61 | 4.69 | **8.33** | 8.73 | 20/20 | 1.00 |

**120 of 120 mux rows hash-verified.**

## Verdicts

`bench/verdict.sh bench/2026-09-16/a5b973a97 bench/2026-09-16/a5b973a97`, median MB/s, wire/goodput
in parentheses where it is not 1.00–1.01:

| set | down 10 (bar 5.00) | up 10 (bar 7.52) | down 50 (bar 5.43) | up 50 (bar 10.32) |
|---|---|---|---|---|
| tunnels-2 | 4.54 | 7.25 (96 %) | **7.37 PASS** | 9.07 (88 %) |
| tunnels-3 | 4.71 | 5.57 | **7.00 PASS** | 9.11 (88 %) |
| legs-2 | **5.33 PASS** (1.03) | 4.64 (1.02) | **7.05 PASS** (1.06) | 8.81 |
| legs-3 | **5.14 PASS** (1.08) | 5.09 (1.12) | 6.05 **(1.36)** | 8.67 |
| compose-T2xL2 | 3.47 (1.10) | 4.53 (1.02) | **6.51 PASS** (1.03) | 8.94 |
| compose-T2xL1 | 4.61 | 4.69 | **8.33 PASS** | 8.73 |

**The 50 MB download passes on every set except legs-3**, and legs-3 fails on the amplification gate
(wire/goodput 1.36), not on rate — 6.05 is still above the 5.43 bar. The 10 MB download passes only
on the two pinned legs sets. **Every upload cell fails**: the fresh direct-stcpr uplink bars, 7.52
and 10.32, are the best the rig has measured all campaign, and no mux shape reaches them.
tunnels-2 comes closest at 96 % and 88 %.

## #4983 fixed the ranking, and the auto-dial now picks what the operator would pin

`mux-tunnels-2.mux_events.json` has one `dial_decision`, and it is the whole point of the run:

> `diversify: 1 sibling group(s) to 022716fb:3, excluding first-hop tp(s) b414796d and 1 first-hop
> peer(s); … excluding 1 same-LAN first hop(s) (share our uplink): 0be8b6f0; ranked by path latency:
> 95839ad0=32+96ms, f1012467=132+0ms, 9cd5f9a8=152+9ms, 588f33c8=159+24ms, fdab37dd=140+46ms, … ;
> chose 95839ad0; oracle: path over a free first hop; first hop 95839ad0`

The LAN neighbour `0be8b6f0` that wrecked campaign19 is excluded by name, the ranking is now
`first-hop + second-hop` latency, and the winner is `95839ad0` — Atlanta
`0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13`, which is also the 50 MB
download bar and one half of the hand-pinned 2x1 composition. The list here is 35 candidates deep;
tunnels-3's second decision ranks **164**, excludes four same-LAN first hops, and chose `f1012467` — Frankfurt
`0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb`. Both picks are routes an
operator would have pinned by hand.

The shapes actually measured:

| set | groups | first-hop tps |
|---|---|---|
| tunnels-2 (auto) | 49159, 49160 | `b414796d` direct + `95839ad0` Atlanta |
| tunnels-3 (auto) | 49162, 49163, 49165 | + `f1012467` Frankfurt |
| legs-2 (pinned) | 49168 | `fdab37dd` Amsterdam + `95839ad0` Atlanta |
| legs-3 (pinned) | 49170 | + `f1012467` Frankfurt |
| compose-T2xL2 (pinned) | 49172 = Ams+Atl, 49173 = Fra+Sin (`ac40965a`) | four |
| compose-T2xL1 (pinned) | 49175 = Ams, 49176 = Atl | two |

Every group's dst_port was constant across its set.

## How the bytes actually split

tunnels-2's 50 MB downloads stripe across both tunnels on every row — Atlanta/direct by `recv_delta`
of 58/42, 67/33, 67/33, 75/25, 67/33 — at w/g 1.01, and its 10 MB downloads split 42/58. Its
**uploads do not split**: rows 16–20 put all 50 MB on a single tunnel (four on direct, one on
Atlanta), because a lone POST takes the lowest-latency tunnel by policy (#4972/#4978). That is why
every upload cell is a single-route number with mux overhead on top, and why 88 % of the direct bar
is the honest ceiling for the upload cells as the scheduler stands.

compose-T2xL1 splits its 50 MB downloads 42/58, 17/83, 25/75, 25/75, 33/67 (Atlanta/Amsterdam) and
takes the run's best single cell, 8.33.

## Churn and counters

| set | events | parks | promotes | wedges |
|---|---|---|---|---|
| tunnels-2 | 5 | 0 | 0 | 0 |
| tunnels-3 | 8 | 0 | 0 | 0 |
| legs-2 | 11 | 2 | 3 | 0 |
| legs-3 | 24 | 8 | 9 | 0 |
| compose-T2xL2 | 26 | 6 | 7 | 0 |
| compose-T2xL1 | 9 | 0 | 1 | 0 |

**No reorder wedge anywhere in the run**, on either end — `wedges` is 0 in every
`.exit-recovery.tsv` snapshot as well as our own `.recovery.tsv`. Both ends' per-row counters were
captured for 119 of 120 rows; the miss is tunnels-2 row 17, where the exit snapshot timed out after
40 s and an empty array was recorded.

## What this run decides

**The `tunnels=2` default is confirmed as the shipped policy.** With no pins, on the shipping
binary, it passes the 50 MB download bar (7.37 vs 5.43, w/g 1.01, 20/20 hashes), reaches 96 % of the
direct uplink bar on 10 MB up and 88 % on 50 MB up, and — the thing campaign19 could not do — picks
Atlanta and Frankfurt by itself. A third tunnel buys nothing: tunnels-3 is within noise of tunnels-2
on three cells and 23 % worse on 10 MB up. Criterion 4 is still not met: the 2x2 composition (6.51 on
the 50 MB download) is below both two tunnels alone (7.37) and two legs alone (7.05).

Two things remain in the way, and neither is leg- or tunnel-count-shaped. The first is the
**per-object prelude**: 10 MB is three 4 MiB chunks, so the fixed exit round trips are a fifth of the
transfer, and only the pinned legs sets clear that cell. The second is **route volatility** — the
50 MB download bar was 8.84, then 6.63, then 5.43 in three consecutive measurement windows, and the
direct downlink lost 59 % of itself between the morning's references and this run's drift probe. A
policy that ranks routes once, at dial time, is ranking against a number that will be wrong within
the hour.

## Next phase

The user's direction, verbatim:

> "route ranking before dialing isn't the best approach, just dial / set up the routes and hold them
> in standby, that would be the best approach and the only way to have routes that can be switched in
> in an instant."

The ranked pre-dial shipped in #4981/#4983 is therefore an **interim step**: it makes the default
safe to ship and it demonstrably picks good routes, but it commits to a ranking taken before any
bytes move. The next phase dials a **standby pool** — routes set up and held, not carrying traffic —
and switches on **live measurement** of the legs that are actually running, so a route that degrades
mid-transfer is replaced in an instant rather than at the next dial. Campaign Results live in
`docs/design/route-multiplexing-test-plan.md`.
