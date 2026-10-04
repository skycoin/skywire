// Package wisp speaks Wisp, the protocol browser-side networking stacks use to
// multiplex TCP and UDP streams over one WebSocket
// (https://github.com/MercuryWorkshop/wisp-protocol), versions 1 and 2,
// including version 2's UDP extension.
//
// A Server provides a Wisp backend: over HTTP (ServeHTTP), or over a conn you
// already have (ServeConn), sending each stream to an Egress — the host's own
// network (DirectEgress) or a SOCKS5 proxy (SocksEgress, with UDP through
// ASSOCIATE). A Client consumes one: Dial by URL, or DialConn over an existing
// conn, then DialContext for TCP and DialUDP for datagrams. A Client is itself
// an Egress, so one Wisp server can relay through another, and a
// proxy.ContextDialer, so anything that takes a SOCKS5 dialer takes it.
// SocksServer puts a SOCKS5 front on a Client.
//
// The client builds for js/wasm, where it dials through the page's own
// WebSocket. Framing over a byte stream (frames.go) lets a session run over
// any net.Conn, such as a virtual loopback in a browser tab, where nothing can
// listen for a WebSocket.
//
// Extracted from skywire (https://github.com/skycoin/skywire), where it was
// pkg/wisp and backs `skywire cli wisp serve`.
package wisp
