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
