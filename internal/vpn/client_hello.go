package vpn

import "net"

type ClientHello struct {
	UnavailablePrivateIPs []net.IP `json:"unavailable_private_ips"`
}
