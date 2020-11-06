//+build !linux

package vpn

import (
	"errors"
	"net"
)

var (
	errServerMethodsNotSupported = errors.New("server related methods are not supported for this OS")
)

// AllowSSH allows all SSH traffic (via default 22 port) between `src` and `dst`.
func AllowSSH(_, _ net.IP) error {
	return errServerMethodsNotSupported
}

// BlockSSH blocks all SSH traffic (via default 22 port) between `src` and `dst`.
func BlockSSH(_, _ net.IP) error {
	return errServerMethodsNotSupported
}

// AllowIPToLocalNetwork allows all the packets coming from `source`
// to private IP ranges.
func AllowIPToLocalNetwork(_ net.IP) error {
	return errServerMethodsNotSupported
}

// BlockIPToLocalNetwork blocks all the packets coming from `source`
// to private IP ranges.
func BlockIPToLocalNetwork(_ net.IP) error {
	return errServerMethodsNotSupported
}

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	return "", errServerMethodsNotSupported
}

// GetIPv4ForwardingValue gets current value of IPv4 forwarding.
func GetIPv4ForwardingValue() (string, error) {
	return "", errServerMethodsNotSupported
}

// GetIPv6ForwardingValue gets current value of IPv6 forwarding.
func GetIPv6ForwardingValue() (string, error) {
	return "", errServerMethodsNotSupported
}

// SetIPv4ForwardingValue sets `val` value of IPv4 forwarding.
func SetIPv4ForwardingValue(_ string) error {
	return errServerMethodsNotSupported
}

// SetIPv6ForwardingValue sets `val` value of IPv6 forwarding.
func SetIPv6ForwardingValue(_ string) error {
	return errServerMethodsNotSupported
}

// EnableIPv4Forwarding enables IPv4 forwarding.
func EnableIPv4Forwarding() error {
	return errServerMethodsNotSupported
}

// EnableIPv6Forwarding enables IPv6 forwarding.
func EnableIPv6Forwarding() error {
	return errServerMethodsNotSupported
}

// EnableIPMasquerading enables IP masquerading for the interface with name `ifcName`.
func EnableIPMasquerading(_ string) error {
	return errServerMethodsNotSupported
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(_ string) error {
	return errServerMethodsNotSupported
}
