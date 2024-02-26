package vpn

import "net"

// ClientHello is a message sent by client during the Client/Server handshake.
type ClientHello struct {
	UnavailablePrivateIPs []net.IP `json:"unavailable_private_ips"`
}

// ServerHello is a message sent by server during the Client/Server handshake.
type ServerHello struct {
	Status     HandshakeStatus `json:"status"`
	TUNIP      net.IP          `json:"tun_ip"`
	TUNGateway net.IP          `json:"tun_gateway"`
}
