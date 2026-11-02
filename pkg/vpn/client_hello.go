// Package vpn pkg/vpn/client_hello.go c4-app-vpn
package vpn

import (
	"net/netip"
)

// ClientHello is a message sent by client during the Client/Server handshake.
type ClientHello struct {
	UnavailablePrivateIPs []netip.Addr `json:"unavailable_private_ips"`
}
