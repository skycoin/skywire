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

Change the import and the client side is the same everywhere. The server side
differs in how methods are registered, because TinyGo cannot run `net/rpc`'s
reflection dispatch (it cannot enumerate or call methods). On native builds, use
`Register`:

```go
import "github.com/0magnet/gobrpc"

srv := gobrpc.NewServer()
srv.Register(new(Arith))
go srv.ServeConn(conn)

c := gobrpc.NewClient(conn)
var reply int
err := c.Call("Arith.Multiply", Args{7, 8}, &reply)
```

Under TinyGo, register a reflection-free handler with `HandleFunc` instead. It
decodes exactly one gob argument and returns the reply:

```go
srv := gobrpc.NewServer()
srv.HandleFunc("Arith.Multiply", func(dec *gob.Decoder) (any, error) {
	var a Args
	if err := dec.Decode(&a); err != nil {
		return nil, err
	}
	return a.A * a.B, nil
})
go srv.ServeConn(conn)
```

`HandleCall` is the same but takes a `DecodeFunc` that decodes the argument and
returns a `CallFunc`; the call then runs in its own goroutine, so a blocking call
does not hold up the connection. `HandlerFunc`, `CallFunc`, `DecodeFunc`,
`HandleFunc` and `HandleCall` exist only in TinyGo builds. The wire format is
unchanged, so a `HandleFunc` server still serves a stdlib `net/rpc` client.

Exposed surface: `NewClient`, `NewServer`, `Dial`, `Client.Go/Call/Close`,
`Server.Register/RegisterName/ServeConn`, the `Call` value, `ServerError` and
`ErrShutdown`. On native builds these are type aliases and vars pointing at `net/rpc`, so `gobrpc.Client`
*is* `rpc.Client` and the two are interchangeable.

Anything outside that subset — `HandleHTTP`, `ServeCodec`, custom codecs — is
deliberately absent, because it is what pulls `net/http` back in.

## gobimpl

`github.com/0magnet/gobrpc/gobimpl` is usable on its own if you want the
reimplementation unconditionally. It imports only `encoding/gob`, `reflect`,
`net`, `io`, `errors`, `strings` and `sync` — all TinyGo-portable — so it
compiles in both worlds. Its tests (`gobimpl/gobimpl_test.go`) run on native. It
also has `Server.Accept(net.Listener)`, and its `Register` is reflection-based, so
use `HandleFunc` under TinyGo.

## Notes

Standard library only. `ErrShutdown` matches `rpc.ErrShutdown`'s message, so
callers comparing on message text keep working.

Extracted from [skywire](https://github.com/skycoin/skywire), where the router's
RPC has to run both natively and inside a browser tab.

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
go run github.com/loov/goda@latest graph github.com/0magnet/gobrpc/... | dot -Tsvg -o docs/gobrpc-goda-graph.svg
```

![Dependency Graph](docs/gobrpc-goda-graph.svg "github.com/0magnet/gobrpc Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                               4            125            170            684
Makefile                         1             22             52            111
YAML                             1              0              7             98
Markdown                         1             30              0             97
Bourne Shell                     1              8             17             31
-------------------------------------------------------------------------------
TOTAL                            8            185            246           1021
-------------------------------------------------------------------------------
```
