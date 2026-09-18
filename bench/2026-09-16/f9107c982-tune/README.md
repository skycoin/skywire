# f9107c982-tune — first bandit tuning run (chain Y)

Binary: f9107c982 (the chain X deploy reused; bench from develop 65d4e646b = `bench/tune`,
#5015). No redeploy — every round is a live `proxy settings` change.
Ran: 2026-09-18 03:43Z–04:28Z, chain Y (`scratchpad/chainY.log`).
Method: Thompson sampling, 12 rounds, one `run-mux.sh` call per round, objective
`mux-tunnels-2/50up:ratio,mux-tunnels-2/10up:ratio`. Per-round dirs under `tune/`.

## Arm table (tune/tune.tsv)
```
upload.chunk_bytes              n       mean   +-stderr best?
  1MiB                          0          -          -
  2MiB                          1     0.8933          -
  4MiB                          3     1.1576     0.2071 <- best
upload.concurrency              n       mean   +-stderr best?
  2                             2     0.8730     0.0189 <- best
  4                             1     0.5991          -
  8                             1     0.6717          -
chunk.max_bytes                 n       mean   +-stderr best?
  2MiB                          2     0.8313     0.0409 <- best
  4MiB                          0          -          -
  8MiB                          2     0.7324     0.0504
incumbent:
SETTINGS="upload.chunk_bytes=4MiB upload.concurrency=2 chunk.max_bytes=2MiB"
```

Decided: with 12 rounds the tuner's incumbent is the shipped default on two of three knobs
and its one preference (`chunk.max_bytes=2MiB`) is inside its own stderr — the upload gap
is not reachable by tuning these three knobs, so the effort moved back to the striping and
snub code paths.
