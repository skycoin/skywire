# Chunked, resumable, striped uploads — design (read-only study at 6745065a5)

## A. Map

### A1. How a request is classified today (client, pkg/skysocks)
| Step | Location | Behaviour |
|---|---|---|
| Accept → tunnel pick | client.go:1007,1030 `l.Accept` → `pickSession()` → `pickSessionFor(pickAny)` (client.go:812,861) | ONE tunnel per browser conn, lowest `rtt × (streams+1)`; the pick precedes any knowledge of the method (comment client.go:440-447) |
| Port gate | client.go:1336 `c.rs.enabled && c.isPlainPort(target)` → `serveHTTPRangeSplit`; else `splicePrefixed` (client.go:1343) | `isPlainPort` (rangesplit.go:114-120) matches exactly ONE port: `rangePlainPort()` = `rs.plainPort` or 80 (rangesplit.go:105-110). `--range-port 18080` (proxy.go:91, skysocks-client.go:149/189/417) REPLACES 80, it does not add to it |
| Head peek | rangesplit.go:924-963 `peekRequestHead` (64 KiB cap, `rsClassifyTimeout` 5 s) | Refuses to split if bytes trail the header block (line 960). curl's `Expect: 100-continue` head arrives alone, so a POST head IS cleanly peekable |
| Splittable? | rangesplit.go:966-980 `splittableRequest` | `GET` only, HTTP/1.1, no `Range`, no `Upgrade`. **A POST returns false here** |
| Where the POST goes | rangesplit.go:203 `if req == nil { return host, reqHead, false }` → rangesplit.go:136 `splicePrefixed(conn, stream, doSplice)` (rangesplit.go:811-835) | One yamux stream, one tunnel, body written straight through. No chunking, no retry, no resume |
| The split machinery a GET gets | `startChunkFetches` 349-378 (`sem`=concurrency 8, `mem`=2×concurrency, `stop`), `writeInOrder` 406-425, `fetchChunkRetry`/`retryWithBudget` 474-515, `openChunkStream` 522-540, `guardTunnel`/`tunnelGuard` 552-591, `rescueTail` 444-469, `fetchChunk` 676-727 (`exitConnectPipelined` = 1 RTT) | Defaults rangesplit.go:62-86: chunk 4 MiB, concurrency 8, `rsChunkRetryBudget` 15 s, `rsFreeRetries` 8 |

### A2. How the client learns a stream died, and how fast
| Mechanism | Location | Latency |
|---|---|---|
| `guardTunnel` watches `sess.CloseChan()`, closes the stream | rangesplit.go:561-573 | Immediate on session close; unblocks a parked read **or write** |
| `g.err()` re-checks `sess.IsClosed()` and relabels the error `errSessionClosed` | rangesplit.go:584-591, var at :97 | yamux usually returns its own shutdown error first — that is why the re-check exists |
| `retryWithBudget` free retry: no backoff, no budget charged | rangesplit.go:485-515 | Costs one round trip; measured recovery below |
| Measured (`bench/2026-09-16/6745065a5-degrade/mux-degrade-tunnels-2.cut.tsv`) | downloads rows 1-3 | ttfb after cut **0.626 / 1.192 / 1.058 s**, hash_ok 3/3 |

### A3. The loadtest sink (cmd/skywire-cli/commands/proxy/loadtest.go) — ours, extensible
| Piece | Lines | Notes |
|---|---|---|
| `loadtest serve` mux | 99-101 | `/upload` → `loadtestUpload`; `/` → `loadtestFixed` when `?bytes=N` |
| `loadtestUpload` | 441-454 | POST/PUT only, `io.Copy(sha256, r.Body)`, answers `{"bytes":N,"sha256":…}`. **No offset, no ack, no resume, no idempotence** |
| `loadtestFixed` | 338-393 | Download side: `Accept-Ranges: bytes`, honours one `Range` via `parseByteRange` (398-436), `X-Sha256` = whole-object sum |
| `loadtestSum` | 305-333 | Cached whole-object hash, `loadtestChunk` 256 KiB pattern granularity (266-287) |
| Bench upload invocation | `bench/run-degrade.sh:248-250` | `curl -X POST --data-binary @payload $sink/upload` through SOCKS5 → hits the non-split path in A1 |

### A4. The measured "before" (3/3 failures, same cut that downloads survive)
| Row | dir | got / 50 MB | http | hash_ok | rg_ports after cut |
|---|---|---|---|---|---|
| t1 | up | 30 408 704 | 100 | 0 | 49217,49219 (rebuilt) |
| t2 | up | 45 023 232 | 100 | 0 | 49220,49223 (rebuilt) |
| t3 | up | 43 843 584 | 100 | 0 | 49224,49226 (rebuilt) |
| t1-t3 | down | 50 000 000 | 200 | 1 | survived, 0.6-1.2 s ttfb |

`http=100` = curl only ever saw the `100 Continue`; the stream died mid-body and the route group was rebuilt.

### A5. Send window — is it the limiter for striping? (pkg/router/route_mux.go:1945-1951)
`ecfMinWindowBytes` 128 KiB · `ecfMaxWindowBytes` **8 MiB per leg** · `ecfWindowMargin` 2.0 · `windowRefreshInterval` 100 ms (doubling per refresh) · `sendWindowWaitMax` 250 ms (`waitSendWindow` 1984-2007); cwnd = delivered × max(first-hop baseline, send→ack) × margin (route_mux.go:1896-1903, #4972).
**Conclusion:** 4 MiB chunks × 2 in flight per tunnel = 8 MiB = exactly the per-leg ceiling. Concurrency 2/tunnel is safe; >2 per tunnel at 4 MiB parks writers at the window. Keep `chunkSize × per-tunnel-inflight ≤ 8 MiB`.

### A6. Generic uploads — the honest answer
| Case | Can it stripe? | Can it resume after a cut? |
|---|---|---|
| Our sink / any origin honouring `Content-Range` PUT or tus | Yes | Yes |
| Arbitrary internet POST | **No** — no offset addressing exists to send chunk *k* independently | **No** — the server has consumed an unknown prefix; re-POSTing may double-apply |
| Arbitrary POST, body ≤ replay cap, no response byte seen | No | Replay the whole request on a surviving tunnel (bounded) |
Striping/resume is therefore an **opt-in-host feature**, never inferred. Silently sending a partial body to a server that did not opt in is data corruption; the design must not allow it.

## B. Design (minimal)

### B1. Sink extension — `loadtest serve` (new handlers next to loadtestUpload:441)
| Item | Choice |
|---|---|
| Advertise | `HEAD`/`OPTIONS /upload` → `Accept-Ranges: bytes` + **`X-Chunked-Upload: bytes`**. This header is the ONLY opt-in signal the client accepts |
| Verb | `PUT /upload?id=<opaque>&bytes=<N>` with `Content-Range: bytes s-e/N` |
| Ack | `200` once the chunk is inside the contiguous prefix, `202 Accepted` while it is only held in the reorder window; both echo `Content-Range` + `X-Upload-Received: <contiguous prefix bytes>`. A chunk is durable only on `200` (or on an `X-Upload-Received` past its end) — a held chunk can still be evicted to admit the frontier, so the sender keeps it until then |
| Reassembly + hash once | Rolling `sha256` over the **contiguous prefix** only; out-of-order chunks held in a bounded reorder window keyed by start offset. Hash is computed exactly once, incrementally — never a second pass, never the whole object in memory |
| Memory bound (exit is 2c/4G, cf. #4252 OOM) | `--upload-window` default **16 MiB** (= 2 × client concurrency × chunk). A chunk beyond the window → `503` + `X-Next-Offset`; sessions capped (8) and GC'd after 60 s idle |
| Idempotent duplicates | Range already absorbed into the prefix → read-and-discard, `200` + `X-Upload-Received` (no re-hash). Duplicate of a *held* chunk → overwrite in place. Overlapping-but-unequal range → `409` |
| Finalize | The chunk whose ack makes the prefix reach N answers the existing `{"bytes":N,"sha256":…}` JSON. `GET /upload/status?id=` returns the same counters for a resume probe |
| Compatibility | Plain `POST /upload` with no `id`/`Content-Range` keeps the current single-stream behaviour — it stays the generic-POST control arm |

### B2. Client striped-upload path (pkg/skysocks/rangesplit.go, mirroring the download)
| Stage | Design |
|---|---|
| Classification | New `splittableUpload(req)` beside `splittableRequest`:980 — method POST/PUT, HTTP/1.1, no `Upgrade`, **known `Content-Length` ≥ `rsUploadMin` (8 MiB)**. Chunked transfer-encoding → not splittable |
| Opt-in probe | On stream0, pipelined behind the CONNECT exactly as `injectRange`:182 does: a `HEAD /upload…`. `X-Chunked-Upload: bytes` present → stripe; absent/any error → B3. One overlapped RTT, no extra tunnel |
| Chunking | `rsUploadChunkSize` = `rs.chunkSize` (4 MiB); `rsUploadConcurrency` **2** (not 8 — buffers live on both client and exit, and A5 caps per-tunnel inflight at 8 MiB) |
| Buffering | Read the browser conn sequentially into chunk buffers behind a `mem` gate identical to `chunkFetches.mem`:336 — at most `2 × concurrency` buffers alive ⇒ **16 MiB ceiling on a small visor**, independent of body size. A buffer is freed only on its 2xx |
| Per-chunk stream | `openChunkStream()`:522 unchanged in shape, but a new `pickSend` in `pickDir`:447 — capacity-weighted on the tunnel's SENT-byte meter (`countingConn.wr`, client.go:1327), falling back to `pickRecv`'s rule with no send samples |
| Retry on a surviving tunnel | `guardTunnel`+`g.err()` wrap the chunk's **write** as well as its read; a mid-write death becomes `errSessionClosed` → `retryWithBudget` free retry on a live tunnel. This is verbatim the mechanism that gives downloads 0.6-1.2 s |
| Ordering / bookkeeping | Chunks may land in any order (`Content-Range` addresses them). Client tracks an `acked` set + the sink's `X-Upload-Received`; done when every chunk is acked. The browser sees ONE response: the sink's final status line + JSON |
| Degrade (mirror of `rescueTail`:444) | On budget exhaustion, send the REMAINING chunks sequentially on the one live tunnel via the same protocol (the sink already holds the prefix — no restart). Only an all-tunnels-down window fails the upload |
| Criterion 2 | Identical striping to downloads: 12 chunks of 50 MB over 2 tunnels, `pickSend` spreading them; two concurrent uploads both spread, so neither is a lone stream stacked by `pickAny` |

### B3. Generic POST replay buffer (no opt-in)
| Rule | Value |
|---|---|
| Cap | `rsUploadReplayMax` **8 MiB** (`Content-Length` known and ≤ cap) |
| Behaviour | Buffer the body; on tunnel death **before any response byte is read**, reopen on a surviving tunnel (`pickAny`, dead tunnel skipped) and replay head+body. At most 2 replays |
| Refusal | `Content-Length` > 8 MiB or unknown, or a response byte already seen → fail exactly as today. No regression, no new corruption surface |
| Honesty | A replayed POST CAN double-apply if the origin consumed the body before the tunnel died. Documented in the flag help; that is the price of surviving a generic POST at all |

## C. PR split (each: `$S/smoke.sh` 6/6 before merge, then the degrade set)
| PR | Scope | Before | After (gate) |
|---|---|---|---|
| 1 | Sink only: `X-Chunked-Upload` advertise, offset-addressed `PUT`, per-chunk ack, prefix hashing + bounded reorder window, idempotent duplicates, status/finalize | `loadtestUpload`:441 has no offsets; nothing to test against | Shell harness (curl, 12 ranges in 2 parallel processes, one duplicate, one out-of-order) reproduces the 50 MB sum; exit RSS measured via pty stays flat (`--upload-window` 16 MiB) |
| 2 | Client striped upload: classification, probe, chunking, `mem` gate, per-chunk stream + `errSessionClosed` retry, degrade, ack bookkeeping | degrade up rows: 30/45/44 MB of 50, `http=100`, hash_ok 0, 3/3, rg rebuilt | hash_ok 1 3/3; time-to-next-acked-chunk after the cut < 2 s; no rg rebuild; `run-mux.sh` 50 MB up within 5 % of the best `a8c3b6486` upload reference; two concurrent uploads ≥ sum of the two best |
| 3 | `pickSend` meter + generic-POST replay buffer (8 MiB) | a ≤8 MiB POST to a non-opt-in origin dies on the cut | ≤8 MiB POST survives 3/3; >8 MiB documented as unchanged; upload striping no longer inherits the recv meter |

PR 2 can merge without 3 (it uses `pickRecv` until then); PR 1 must land first since PR 2's gate measures against it.

## D. Risks
| Risk | Mitigation |
|---|---|
| **Classification false positive** — chunking to a server that did not opt in corrupts data | Opt-in is a single explicit header; absence ⇒ B3. Never infer from a 2xx |
| Partial-write duplicates (a chunk half-written, then re-sent) | The sink is offset-addressed and idempotent by range; a partial PUT never advances the prefix hash (`Content-Length` short ⇒ 400, chunk retried) |
| Sink memory on a 2c/4G exit (#4252 history) | Prefix hashing means O(window), not O(object); `--upload-window` 16 MiB, 8 sessions, 60 s GC, 503 back-pressure |
| Standby-pool promoter interaction | A promoted standby leg changes the tunnel's carrier mid-chunk; `guardTunnel` only fires on session close, so a leg swap is invisible (correct). But `pickSend` reads a meter whose samples came from the pre-promotion leg — treat a promotion as a meter reset, as `#4965` does for stale idle tunnels |
| The 5 % single-upload bar | Chunking adds one pipelined RTT + an ack per chunk (12 for 50 MB). Overlapped at concurrency 2 it should hide, but if it does not, raise `rsUploadChunkSize` to 8 MiB and drop per-tunnel inflight to 1 (A5 ceiling) |
| Replay double-apply | Bounded to ≤8 MiB, zero-response-bytes only, 2 attempts, documented |
| `Expect: 100-continue` | curl sends it for large bodies (that is why `http=100` is the observed failure); the striped path must answer it locally once the probe succeeds, and must NOT forward the browser's 100-continue wait to every chunk stream |
