package vpn

import "net"

// ServerHello is a message sent by server during the Client/Server handshake.
type ServerHello struct {
	Status     HandshakeStatus `json:"status"`
	TUNIP      net.IP          `json:"tun_ip"`
	TUNGateway net.IP          `json:"tun_gateway"`
}
