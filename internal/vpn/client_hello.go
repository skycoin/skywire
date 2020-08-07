package vpn

import "net"

// ClientHello is a message sent by client during the Client/Server handshake.
type ClientHello struct {
	UnavailablePrivateIPs []net.IP `json:"unavailable_private_ips"`
	Passcode              string   `json:"passcode"`
}
