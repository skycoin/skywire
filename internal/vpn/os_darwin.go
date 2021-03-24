//+build darwin

package vpn

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return osutil.Run("ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

// ChangeRoute changes current route to `ipCIDR` to go through the `gateway`
// in the OS routing table.
func ChangeRoute(ipCIDR, gateway string) error {
	return modifyRoutingTable("change", ipCIDR, gateway)
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func AddRoute(ipCIDR, gateway string) error {
	if err := modifyRoutingTable("add", ipCIDR, gateway); err != nil {
		var e *osutil.ErrorWithStderr
		if errors.As(err, &e) {
			if strings.Contains(string(e.Stderr), "File exists") {
				return nil
			}
		}

		return err
	}

	return nil
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func DeleteRoute(ipCIDR, gateway string) error {
	return modifyRoutingTable("delete", ipCIDR, gateway)
}

func modifyRoutingTable(action, ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return osutil.Run("route", action, "-net", ip, gateway, netmask)
}
