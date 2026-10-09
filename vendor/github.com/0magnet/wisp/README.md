# wisp

A Go implementation of [Wisp](https://github.com/MercuryWorkshop/wisp-protocol),
the protocol browser-side networking stacks use to carry TCP and UDP streams
over one WebSocket: server and client, versions 1 and 2, including version 2's
UDP extension.

Extracted from [skywire](https://github.com/skycoin/skywire)'s `pkg/wisp`;
skywire now imports this module for `skywire cli wisp serve`, the Wisp backend
that [LinuxOnTab](https://linuxontab.com) can run its guest's network through,
and for the visor's embedded wisp server.

## Serving

A `Server` takes each stream a client opens to an `Egress`: `DirectEgress`
dials the host's own network, and `SocksEgress` hands it to a SOCKS5 proxy,
with UDP through `UDP ASSOCIATE` where the proxy relays datagrams (and DNS
over TCP where it does not).

```go
srv, err := wisp.NewServer(wisp.Config{Egress: &wisp.DirectEgress{}})
if err != nil {
	log.Fatal(err)
}
http.Handle("/wisp", srv)
log.Fatal(http.ListenAndServe("127.0.0.1:6001", nil))
```

`ServeConn` runs a session over any `net.Conn` instead, with Wisp's packets
length-prefixed on the byte stream — which is how a backend reaches a guest in
a browser tab, where nothing can listen for a WebSocket.

## Dialing

A `Client` opens a session against a Wisp server and multiplexes streams over
it: `DialContext` for TCP, `DialUDP` for datagrams (when the server offers the
UDP extension, which `UDPSupported` reports).

`wisp.DialConn(ctx, conn, cfg)` runs the same session over a `net.Conn` you
already have, instead of dialing a URL.

```go
c, err := wisp.Dial(ctx, wisp.ClientConfig{URL: "ws://127.0.0.1:6001/wisp"})
if err != nil {
	log.Fatal(err)
}
conn, err := c.DialContext(ctx, "tcp", "example.com:80") // a net.Conn
d, err := c.DialUDP(ctx, "1.1.1.1", 53)                  // WriteDatagram / ReadDatagram
```

The client builds for `GOOS=js GOARCH=wasm` and under TinyGo, where it dials
through the page's own WebSocket. A `Client` is also an `Egress`, so one Wisp
server can relay through another, and a `proxy.ContextDialer`, so anything that
takes a SOCKS5 dialer takes it; `SocksServer` puts a SOCKS5 front on it.

## Logging

Everything this package logs is a debug detail of a session or stream, through
`log/slog`: `Config.Log`, `ClientConfig.Log` and `SocksServer.Log` take a
`*slog.Logger`, and default to `slog.Default()`.

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run
GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph github.com/0magnet/wisp/... | dot -Tsvg -o docs/wisp-goda-graph.svg
```

![Dependency Graph](docs/wisp-goda-graph.svg "github.com/0magnet/wisp Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                              22            599            802           4179
YAML                             1              0              7             98
Makefile                         1             19             34             89
Markdown                         1             21              0             73
Bourne Shell                     1              8             16             30
-------------------------------------------------------------------------------
TOTAL                           26            647            859           4469
-------------------------------------------------------------------------------
```
