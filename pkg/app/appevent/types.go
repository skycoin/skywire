// Package appevent pkg/app/appevent/types.go
package appevent

// AllTypes returns all event types.
func AllTypes() map[string]bool {
	return map[string]bool{
		TCPDial:  true,
		TCPClose: true,
	}
}

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
