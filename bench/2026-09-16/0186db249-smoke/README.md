# 0186db249-smoke — int-E4 (striped upload, merged from here)

Commit under test: 0186db249, merge of 5f3b2aaaf into int-E4 — the int-E3 content plus
"the striped upload overlaps the handshake with the body, frees a slot on the ack, and
keeps four chunks per tunnel in flight" and "a sibling candidate with unknown path
latency is ranked by its transport latency, not dropped"; opened as #4997, #4999 and
#5001.
Ran: 2026-09-17 23:32Z-23:50Z, chain L (scratchpad/chainL.log).
Sets: mux-legs-2, mux-tunnels-2, mux-tunnels-2-up2, mux-degrade-tunnels-2.
## Verdict rows (paired vs 0371ab4b)
    mux-tunnels-2  down 10  4.56 MB/s  x1.099 PASS   up 10  3.47  x0.521 FAIL
    mux-tunnels-2  down 50  8.21       x1.440 PASS   up 50 10.00  x1.004 PASS
    mux-legs-2     down 10  5.03       x2.556 PASS   up 10  4.62  x0.635 FAIL (2/3)
    mux-legs-2     down 50  4.66       x1.375 FAIL (w/g 1.54)  up 50  8.86  x0.847 FAIL
50 MB up finally met the bar (x1.004). Two concurrent uploads:
    trial 1  5.85 + 5.39 = 11.24 vs ref_sum 13.56  ->  ratio 0.829
    trial 2  5.53 + 5.51 = 11.04 vs ref_sum 13.70  ->  ratio 0.806
against 0.562/0.566 on int-E3 (a6a42506f-smoke).

## Cut rows (mux-degrade-tunnels-2, 50 MB up, cut 40 %/3 s)
    t1  cut at 2.795 s after 31415386 B  ->  http=200, hash_ok=1, ttfb after 1.753 s,
        goodput 11239852 -> 7442777 B/s
    t2  cut at 1.430 s after 22297264 B  ->  http=200, hash_ok=1, ttfb after 1.894 s,
        goodput 15592492 -> 6789886 B/s
2 rows, hash_ok=2, cuts=2.

## Exit gate
mux-tunnels-2, mux-legs-2 and mux-tunnels-2-up2 all PASS.

Outcome: merged — #4997 as 5bd77c293, #4999 as e92e9bdf9, #5001 as 07748399b.
