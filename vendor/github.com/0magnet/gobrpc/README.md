# gobrpc

`net/rpc`, except it also compiles for TinyGo's `js/wasm` target.

Go's `net/rpc` imports `net/http` (for its `HandleHTTP` helper), and `net/http`
does not build under TinyGo. That one import is enough to make the whole
package unavailable — even if you only ever call `ServeConn` and never touch
HTTP.

This package is an API shim:

- **Native builds** alias straight to `net/rpc`. Same types, same wire format,
  same concurrency semantics — it *is* the standard library, so there is no
  behavioural change and nothing new to trust.
- **TinyGo builds** alias to `gobimpl`, a dependency-free reimplementation that
  speaks the **identical** gob wire protocol.

```
request:   gob(Request{ServiceMethod, Seq})         then gob(args)
response:  gob(Response{ServiceMethod, Seq, Error}) then gob(reply)
```

Because the framing is the same, a TinyGo client talks to a stdlib `net/rpc`
server and vice versa, with nothing in between. `gobimpl`'s tests assert this
by connecting `gobimpl` to `net/rpc` in both directions over a `net.Pipe`.

## Install

```
go get github.com/0magnet/gobrpc
```

## Use

Drop-in for the common subset — change the import and nothing else:

```go
import "github.com/0magnet/gobrpc"

srv := gobrpc.NewServer()
srv.Register(new(Arith))
go srv.ServeConn(conn)

c := gobrpc.NewClient(conn)
var reply int
err := c.Call("Arith.Multiply", Args{7, 8}, &reply)
```

Exposed surface: `NewClient`, `NewServer`, `Dial`, `Client.Go/Call/Close`,
`Server.Register/ServeConn`, the `Call` value and `ErrShutdown`. On native
builds these are type aliases and vars pointing at `net/rpc`, so `gobrpc.Client`
*is* `rpc.Client` and the two are interchangeable.

Anything outside that subset — `HandleHTTP`, `ServeCodec`, custom codecs — is
deliberately absent, because it is what pulls `net/http` back in.

## gobimpl

`github.com/0magnet/gobrpc/gobimpl` is usable on its own if you want the
reimplementation unconditionally. It imports only `encoding/gob`, `reflect`,
`net`, `io`, `errors`, `strings` and `sync` — all TinyGo-portable — so it
compiles in both worlds and is unit-tested on native in CI.

## Notes

Standard library only. `ErrShutdown` matches `rpc.ErrShutdown`'s message, so
callers comparing on message text keep working.

Extracted from [skywire](https://github.com/skycoin/skywire), where the router's
RPC has to run both natively and inside a browser tab.
