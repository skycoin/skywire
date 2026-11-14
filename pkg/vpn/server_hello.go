// Package vpn pkg/vpn/server_hello.go c3-app-vpn
package vpn

import (
	"net/netip"
)

// ServerHello is a message sent by server during the Client/Server handshake.
type ServerHello struct {
	Status     HandshakeStatus `json:"status"`
	TUNIP      netip.Addr      `json:"tun_ip"`
	TUNGateway netip.Addr      `json:"tun_gateway"`
}
