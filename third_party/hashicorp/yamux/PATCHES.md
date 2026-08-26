# hashicorp/yamux — vendored fork

Source: `github.com/hashicorp/yamux` v0.1.2 (MPL-2.0, see `LICENSE`).

Copied in-module (imported as `github.com/skycoin/skywire/third_party/hashicorp/yamux`)
so a local correctness fix can ship without waiting on an upstream release. The
package name is unchanged (`yamux`), so call sites are identical apart from the
import path.

## Local changes vs upstream v0.1.2

- **`stream.go` — `Stream.Read` / `Stream.write`: return on session shutdown.**
  Upstream's `case <-s.session.shutdownCh:` in each WAIT select has no body and
  falls through to `goto START`. `START` only inspects the per-stream `state`,
  which stays `streamEstablished` when a session is torn down mid-Read/Write
  (common when a dmsg WS/WebRTC carrier drops during an in-flight dial).
  `shutdownCh` is a *closed* channel, so the select re-fires it every iteration,
  re-allocating a timer each pass — a single goroutine pegs the (single) wasm
  thread at ~370k `time.NewTimer`/s and never recovers. Fix: `return 0,
  ErrSessionShutdown` from the `shutdownCh` case in both `Read` and `write`.
  Upstream PR / issue to follow (tracked in skycoin/skywire).

- **`stream.go` — `Stream.Read` / `Stream.write`: don't rely on select fairness
  to observe shutdown.** The return case above fires only when the blocking
  `select` happens to pick `shutdownCh`. `select` chooses uniformly among ready
  cases, so once the session is shut down a lingering — or repeatedly
  re-signaled — `recvNotifyCh` / `sendNotifyCh` token can be chosen instead,
  sending the loop back through `START` with no data / a zero send window and no
  forward progress. The per-stream state stays `streamEstablished` until
  `forceClose` runs, and with no read/write deadline set the retry loop
  allocates nothing, so it never hits a GC safepoint and, on single-threaded
  js/wasm, never yields — one goroutine pegs the only thread and freezes the
  runtime (GC included) for as long as the token keeps arriving. Fix: a
  non-blocking `case <-s.session.shutdownCh: return` / `default:` pre-check at
  the top of each WAIT path so termination is deterministic and independent of
  select fairness and of how often the notify channel is signaled. The pre-check
  is placed after the `START` buffered-data handling so a half-closed stream
  still drains its receive buffer before returning. Regression coverage:
  `stream_shutdown_test.go`.
