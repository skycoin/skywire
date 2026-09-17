# 80f6c5a6c — mux suite with FEC off, window over the feedback delay, split downloads (after #4960, #4961)

References: `../98ff6be85/`. Before rows: `../45905e2f5/` (FEC on by default, window over the ping RTT).
Sets ran through the default `skysocks-client` on :1080 with `--range-port 18080`; legs sets pin `mux width N`.

Verdicts (`bench/verdict.sh ../98ff6be85 .`), median MB/s against the best single-route reference:

| set | down 10 | up 10 | down 50 | up 50 |
|---|---|---|---|---|
| bar (best ref) | 3.37 | 6.49 | 4.43 | 9.91 |
| tunnels-2 | 0.92 | 3.26 | **4.47** | 8.49 |
| tunnels-3 | 1.63 | 3.82 | 3.35 | 7.84 |
| legs-2 | **3.58** | 3.04 | **6.81** | 7.10 |
| legs-3 | 2.57 | 3.01 | **7.51** | 0 (session froze) |
| legs-5 | — | — | — | — (client never re-established a group) |

What the rows say:

- Downloads split across tunnels (carrier columns: rows 1–5 are the 10 MB downloads;
  10 MB = 4 + 4 + 1.6 MiB chunks). The picker (fewest yamux streams, ties → the first
  session = the direct tunnel, the slowest downloader at 2.2 MB/s) lands two 4 MiB chunks on
  the direct tunnel, so the 10 MB download is tail-bound → chunk size is the lever
  (`--range-chunk-kib`, plumbed by #4962).
- Every single route uploads at 9.1–9.9 MB/s: the uplink itself caps near 10 MB/s, so a
  mux can only lose on uploads. tunnels-2 lost to 220 writer parks on a lone leg; the 10 MB
  uploads lost to the window ramp from the 128 KiB floor (both addressed by #4962).
- legs-2 downloads aggregate for real: 6.81 MB/s over 03f57e7c + 0371ab4b against 4.43 for the best single route.
- legs-3 (adding the 470 ms 0255117b path) stormed at once (retransmits 4 → 608 in the first
  10 MB upload, wire/goodput 1.4–2.0) and then FROZE at 34 MB of the first 50 MB upload: a
  lock-order inversion between the window refresh and the SACK handler (goroutine dump in the
  session scratch; fixed by #4962 with a regression test). Every RPC touching the group then
  hung, which is why the set-end exit snapshot and the legs-5 set are empty.
