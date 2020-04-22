package vpn

import "net"

type ServerHello struct {
	Status     HandshakeStatus `json:"status"`
	TUNIP      net.IP          `json:"tun_ip"`
	TUNGateway net.IP          `json:"tun_gateway"`
}
