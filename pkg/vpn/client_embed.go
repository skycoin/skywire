// Package vpn pkg/vpn/client_embed.go c2-app-vpn
//
// In-process embedding entry for the VPN client. The standard construction
// path (NewClient + Serve) is an APP: it reads the appserver environment for
// direct-route IPs, reports status over the app RPC, and dials the server
// through the app client. An embedder — the wasm visor's in-tab VPN instance
// is the driving case — has none of that: it dials the vpn-server route group
// itself and owns the lifecycle. NewClientEmbedded skips the environment and
// RPC coupling entirely (nil appCl; the status setters no-op), and ServeConn
// runs one VPN session over the conn the embedder dialed: handshake, TUN (on
// js the gVisor netstack, netstack_tun.go), and the packet copy loops.
package vpn

import "net"

// NewClientEmbedded constructs a Client for in-process use: no app
// environment, no app RPC, no direct-route management. The zero directIPs
// set is correct for the browser (there is no OS routing table to protect),
// and acceptable for any embedder that manages its own underlay reachability.
func NewClientEmbedded(cfg ClientConfig) *Client {
	return &Client{
		cfg:    cfg,
		closeC: make(chan struct{}),
	}
}

// TunReady reports whether the tunnel device exists — it flips once the
// server handshake assigned an address and the TUN (on js, the netstack) came
// up, which is the earliest NetstackDial can succeed. Embedders poll it to
// learn when a launched session is usable.
func (c *Client) TunReady() bool {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()
	return c.tunCreated
}

// ServeConn runs a single VPN session over an already-dialed connection to
// the vpn-server. It blocks until the session ends (server close, conn
// failure, or Close). The TUN persists across calls so an embedder may
// re-dial and call ServeConn again on reconnect, exactly like the app's own
// serve loop does.
func (c *Client) ServeConn(conn net.Conn) error {
	return c.serveConn(conn)
}
