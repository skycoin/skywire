//+build darwin

package vpn

import (
	"fmt"
	"strconv"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return run("ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func AddRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return run("route", "add", "-net", ip, gateway, netmask)
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func DeleteRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return run("route", "delete", "-net", ip, gateway, netmask)
}
