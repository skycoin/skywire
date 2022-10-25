// Package vpn internal/vpn/const.go
package vpn

const (
	// TUNNetmaskCIDR is a general netmask used for all TUN interfaces in CIDR format (only suffix).
	TUNNetmaskCIDR = "/29"
	// TUNMTU is MTU value used for all TUN interfaces.
	TUNMTU = 1500
)
