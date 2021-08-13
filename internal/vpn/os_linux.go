//+build linux

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
	if err := osutil.Run("ip", "a", "add", ipCIDR, "dev", ifcName); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}

	if err := osutil.Run("ip", "link", "set", "dev", ifcName, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("error setting MTU: %w", err)
	}

	ip, _, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	if err := osutil.Run("ip", "link", "set", ifcName, "up"); err != nil {
		return fmt.Errorf("error setting interface up: %w", err)
	}

	if err := AddRoute(ip, gateway); err != nil {
		return fmt.Errorf("error setting gateway for interface: %w", err)
	}

	return nil
}

// ChangeRoute changes current route to `ip` to go through the `gateway`
// in the OS routing table.
func ChangeRoute(ip, gateway string) error {
	return osutil.Run("ip", "r", "change", ip, "via", gateway)
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func AddRoute(ip, gateway string) error {
	err := osutil.Run("ip", "r", "add", ip, "via", gateway)

	var e *osutil.ErrorWithStderr
	if errors.As(err, &e) {
		if strings.Contains(string(e.Stderr), "File exists") {
			return nil
		}
	}

	return err
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func DeleteRoute(ip, gateway string) error {
	return osutil.Run("ip", "r", "del", ip, "via", gateway)
}
