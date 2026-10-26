# gobrpc

`net/rpc`, except it also compiles under TinyGo.

Go's `net/rpc` imports `net/http` (for its `HandleHTTP` helper), and `net/http`
does not build under TinyGo. That one import is enough to make the whole
package unavailable, even if you only ever call `ServeConn`.

This package aliases `gobimpl`, a dependency-free reimplementation that speaks
the **identical** gob wire protocol. The same code runs under both Go and
TinyGo, so a program behaves the same whichever compiler built it.

```
request:   gob(Request{ServiceMethod, Seq})         then gob(args)
response:  gob(Response{ServiceMethod, Seq, Error}) then gob(reply)
```

Because the framing is the same, a gobrpc client talks to a stdlib `net/rpc`
server and vice versa, with nothing in between. `gobimpl`'s tests assert this
by connecting `gobimpl` to `net/rpc` in both directions over a `net.Pipe`.

## Install

```
go get github.com/0magnet/gobrpc
```

## Use

```go
import "github.com/0magnet/gobrpc"

srv := gobrpc.NewServer()
srv.Register(new(Arith))
go srv.ServeConn(conn)

c := gobrpc.NewClient(conn)
var reply int
err := c.Call("Arith.Multiply", Args{7, 8}, &reply)
```

`Register` uses reflection to find methods, which TinyGo cannot do. Code that
must also build under TinyGo registers a reflection-free handler with
`HandleFunc` instead. It decodes exactly one gob argument and returns the reply:

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
does not hold up the connection. The wire format is unchanged, so a
`HandleFunc` server still serves a stdlib `net/rpc` client.

Exposed surface: `NewClient`, `NewServer`, `Dial`, `Client.Go/Call/Close`,
`Server.Register/RegisterName/HandleFunc/HandleCall/ServeConn`, the `Call`
value, `ServerError` and `ErrShutdown`. These are distinct from the `net/rpc`
types, so a `gobrpc.Client` cannot be passed where an `*rpc.Client` is wanted.

Anything outside that subset (`HandleHTTP`, `ServeCodec`, custom codecs) is
deliberately absent, because it is what pulls `net/http` back in.


## gobimpl

`github.com/0magnet/gobrpc/gobimpl` is usable on its own if you want the
reimplementation directly. It imports only `encoding/gob`, `reflect`,
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
