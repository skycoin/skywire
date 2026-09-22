# skywire cli wisp

[← skywire cli](../README.md)

Serve the Wisp protocol over WebSocket, carrying every stream it is
asked for over skywire via the local SOCKS5 proxy.

Wisp multiplexes many TCP and UDP sockets over a single WebSocket. It is what
browser Linux emulators use to give a guest kernel a network: the page runs the
guest's TCP/IP stack and asks its backend to open connections by name. Serving
it here puts that traffic on a route to a skywire exit rather than through a
central proxy.

Both protocol versions are served. Which one is used is the client's choice: v2
clients open with a Sec-WebSocket-Protocol header and get an INFO exchange
advertising UDP support, v1 clients get the initial CONTINUE straight away.

UDP needs care. SOCKS5 as skysocks implements it has no UDP ASSOCIATE, so a UDP
stream cannot cross an exit as-is. Port 53 is translated to DNS-over-TCP through
the same proxy, which is what lets a guest resolve names without running its own
unbound; every other UDP port is refused rather than quietly leaked to the
clearnet. With --direct, real UDP sockets are used and nothing is refused.

Examples:
  skywire cli wisp                                   # ws://127.0.0.1:6001/wisp over skywire
  skywire cli wisp --addr 0.0.0.0:6001               # serve a LAN, e.g. from a NAS
  skywire cli wisp --direct                          # clearnet egress, for comparison
  skywire cli wisp --socks 127.0.0.1:9050            # some other SOCKS5 proxy

  # LinuxOnTab reads its backend from a query parameter:
  https://next.linuxontab.com/?wisp=ws://127.0.0.1:6001/wisp

## Usage

```
Usage:
  skywire cli wisp



Flags:
  -a, --addr string       address to serve on
                          (default "127.0.0.1:6001")
  -b, --buffer uint32     per-stream client→server buffer, in packets
                          (default 128)
      --dial-timeout int  dial timeout in seconds
                          (default 30)
      --direct            dial the host's own network instead of --socks;
                          nothing is carried over skywire
  -p, --path string       websocket path
                          (default "/wisp")
  -s, --socks string      SOCKS5 proxy to carry streams over — the local
                          skysocks-client by default
                          (default "127.0.0.1:1080")
      --tls-cert string   TLS certificate — serve wss:// instead of ws://
      --tls-key string    TLS key, with --tls-cert

Global Flags:
      --jq string        filter JSON output through a jq/gojq expression
                         (implies --json)
      --json             print output as JSON
      --shape            print the output schema skeleton (zero values, all
                         fields) instead of data
      --tui              browse commands and help interactively
      --via dmsg://<pk>  remote visor target — dmsg://<pk> or `skynet://<pk>`
```
