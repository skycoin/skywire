// Package network pkg/transport/network/ws_attached.go c2-net-transport
package network

import "net"

// attachedConn marks a WS conn that arrived through a hypervisor's /tp/ws
// (wsClient.ServeHTTP) rather than the transport port.
type attachedConn struct{ net.Conn }

// Attached reports whether the transport was opened back to this visor by a
// desk its hypervisor serves, through /tp/ws.
func (c *transport) Attached() bool {
	_, ok := c.rawConn.(attachedConn)
	return ok
}
