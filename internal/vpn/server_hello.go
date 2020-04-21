package vpn

import "net"

type NegotiationStatus int

const (
	NegotiationStatusOK            = 0
	NegotiationStatusIPNotReserved = 1
	NegotiationStatusInternalError = 2
)

type ServerHello struct {
	Status     NegotiationStatus `json:"status"`
	TUNIP      net.IP            `json:"tun_ip"`
	TUNGateway net.IP            `json:"tun_gateway"`
}
