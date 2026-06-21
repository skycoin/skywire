# TinyGo dmsg client (IoT / embedded)

Status: in progress
Motivation: compile the dmsg client under TinyGo so dmsg can run on
microcontrollers / WASI runtimes (IoT), and so the core routing concepts are a
step closer to a port to other platforms (e.g. FPGAs). Related: dmsg-over-WS
(#3189), WASM hypervisor (#3190), `encoding/json` purge / "TinyGo unlocked"
(#2836 — see "What changed since #2836").

## Empirical findings (TinyGo 0.41.1, go1.26 toolchain)

Measured directly, not assumed:

| Concern | Result |
|---|---|
| `encoding/gob` (dmsg handshake/control frames, `types.go`) | **compiles** under TinyGo |
| `encoding/json` | **compiles** |
| `github.com/sirupsen/logrus` (`pkg/logging`) | **compiles** |
| `net/http` on `wasip1` (IoT target) | **compiles** |
| `net/http` on `wasm` (js/browser target) | **broken** — TinyGo stdlib bug (`net/http/roundtrip_js.go: t.roundTrip undefined`). `net/rpc` fails only transitively through this. |
| `quic-go` (any TinyGo target) | **broken** — needs `crypto/tls.QUICEncryptionLevel`, which TinyGo's `crypto/tls` doesn't ship |

### What changed since #2836

#2836 assumed `logrus`/`encoding/json`/reflect couldn't run under TinyGo and built
a js-only stub (`pkg/logging/logging_js.go`). TinyGo 0.41 ships enough `reflect`
that **none of those are blockers anymore**. The lightweight logging stub is
still worth keeping for embedded (smaller binary, no log overhead), but it is no
longer a hard requirement, and it should apply to **all** TinyGo targets, not
just `js`.

## So the real blockers are just two

For a TinyGo **IoT (`wasip1`) dmsg client**:

1. **The logging stub was pinned to `GOOS=js`** by its filename
   (`logging_js.go` → Go's implicit `_js.go` GOOS rule overrides the
   `//go:build tinygo` tag). Under `wasip1`+TinyGo, `pkg/logging` then had *no*
   files → "build constraints exclude all Go files". **Fixed in this PR**:
   renamed to `logging_tinygo.go` so `//go:build tinygo` covers js, wasip1, and
   bare-metal alike. Production (`!tinygo`) and standard-Go js/wasm builds are
   unaffected (they never used the stub).

2. **`quic-go` is woven into the session core.** `*quic.Conn` / `*quic.Stream`
   appear as struct fields and method branches across six files:
   `session_common.go` (`sm.quic *quic.Conn`, `quicAddrConn`, `initQUIC`),
   `stream.go` (`qStr *quic.Stream`, `case sm.quic != nil`),
   `client_session.go` (`makeClientSessionQUIC`), `server_session.go`
   (`makeServerSessionQUIC` + accept loop), `client_sessions.go`
   (`dialSessionQUIC`), `server.go` (`ServeQUIC`). An IoT client needs neither
   QUIC nor WebTransport, but the package won't TinyGo-compile while these
   concrete types are referenced.

WebTransport (`wt.go`) also pulls quic-go + the js-broken `net/http`; it is
server-side + browser-only and simply gets a `//go:build !tinygo` tag.

## Plan

### Phase 1 — logging stub generalization *(this PR)*

`logging_js.go` → `logging_tinygo.go`. Unblocks `pkg/logging` under all TinyGo
targets. Safe, production-neutral. Establishes the rename pattern for any other
`_js.go` TinyGo stubs that appear.

### Phase 2 — extract QUIC behind an interface *(next, test-driven)*

The session core should hold QUIC behind a small interface so the concrete
`quic-go` types live only in `!tinygo` files:

- Define (untagged) a minimal `quicConn` / `quicStream` interface with exactly
  the methods the core uses (`AcceptStream`, `OpenStreamSync`, `CloseWithError`,
  `StreamID`, …). The core structs hold the interface; under TinyGo the field is
  always nil and every `case sm.quic != nil` branch is dead code.
- Move `*quic.Conn`/`*quic.Stream` construction and the
  `makeClientSessionQUIC` / `makeServerSessionQUIC` / `dialSessionQUIC` /
  `ServeQUIC` / `quicAddrConn` implementations into `quic_native.go`
  (`//go:build !tinygo`).
- `wt.go` → `//go:build !tinygo` (quic-go + js-broken net/http; server/browser
  only).

Each step keeps the **regular `go build ./...` + full `pkg/dmsg/...` test suite
green** — this is the most-tested code in the tree, so the bar is no regressions,
proven, before merge.

**Status: done.** `quic_iface.go` / `quic_native.go` / `quic_stub.go` land the
interface + adapters + stubs; the six core files hold `quicConn` / `quicStream`;
`wt.go` is `!tinygo`; `victoria_metrics.go` is `!tinygo` (server-only); and
`pkg/logging` now builds real logrus on all targets (logrus/json compile under
TinyGo 0.41, so the no-op stub — which couldn't satisfy `logrus.FieldLogger` for
the full client — was dropped). `pkg/dmsg/dmsg` is verified quic-free under
`go list -tags tinygo`.

Writing the first dmsg-over-QUIC round-trip test (`quic_test.go`) to validate the
extraction also surfaced **two latent bugs** in the QUIC stream path that no test
had ever exercised, both now fixed: server `forwardRequest` opened the
destination stream via a smux/else-yamux branch (nil-deref panic for a QUIC
destination), and the client accept loop never treated a QUIC `AcceptStream`
error as terminal (spun forever → hung `Close()`).

### Phase 3 — finish the peripheral deps, then prove it + a build target

The remaining TinyGo blocker (for the `wasip1`/IoT target) is **`pkg/netutil`**:
`net.go` uses `net.Interface.Addrs()` (absent in TinyGo's `net`) and the
GOOS-split `DefaultNetworkInterface` has no wasip1 variant. The dmsg client only
needs `netutil`'s Porter + retrier, not the interface-enumeration helpers, so the
fix is a `!tinygo` split + a small tinygo stub for the enum functions (the same
pattern used for QUIC/metrics). Expect a short tail of similar peripheral
packages after it.

Then: a `cmd/` probe (or a `_test` build) and a `Makefile` `dmsg-tinygo` target
(`tinygo build -target wasip1`) that compiles the client, wired into CI so the
TinyGo build can't silently regress. For the browser, a TinyGo `wasm` build
additionally needs `net/http` out of the graph (drop the server serve paths +
metrics http from the client subset) — lower priority than the IoT target.

### Beyond

A TinyGo-clean session core is also the natural seam for a future port of the
routing concepts to non-Go targets (e.g. FPGA): once the wire framing
(`types.go`), Noise handshake, and session state are free of heavyweight runtime
deps, the protocol is specified by code that a HLS/RTL or Rust/Zig
re-implementation can mirror directly.
```
