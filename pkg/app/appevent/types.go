// Package appevent pkg/app/appevent/types.go c2-vis-appsvc
package appevent

// TCPDial represents a dial event.
const TCPDial = "tcp_dial"

// TCPDialData contains net dial event data.
type TCPDialData struct {
	RemoteNet  string `json:"remote_net"`
	RemoteAddr string `json:"remote_addr"`
}

// Type returns the TCPDial type.
func (TCPDialData) Type() string { return TCPDial }

// TCPClose represents a close event.
const TCPClose = "tcp_close"

// TCPCloseData contains net close event data.
type TCPCloseData struct {
	RemoteNet  string `json:"remote_net"`
	RemoteAddr string `json:"remote_addr"`
}

// Type returns the TCPClose type.
func (TCPCloseData) Type() string { return TCPClose }

// NetworkMoved is sent after the visor handled a change of network, so an
// app holding routes or sockets tied to the old one can move them.
const NetworkMoved = "network_moved"

// NetworkMovedData says what triggered the change.
type NetworkMovedData struct {
	Reason string `json:"reason"`
}

// Type returns the NetworkMoved type.
func (NetworkMovedData) Type() string { return NetworkMoved }
