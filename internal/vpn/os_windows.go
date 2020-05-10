//+build windows

package vpn

import (
	"fmt"
	"os"
)

const (
	tunSetupCMDFmt    = "netsh interface ip set address name=\"%s\" source=static addr=%s mask=%s gateway=%s"
	tunMTUSetupCMDFmt = "netsh interface ipv4 set subinterface \"%s\" mtu=%d"
	addRouteCMDFmt    = "route add %s mask %s %s"
	deleteRouteCMDFmt = "route delete %s mask %s %s"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	setupCmd := fmt.Sprintf(tunSetupCMDFmt, ifcName, ip, netmask, gateway)
	if err := run("cmd", "/C", setupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", setupCmd, err)
	}

	mtuSetupCmd := fmt.Sprintf(tunMTUSetupCMDFmt, ifcName, mtu)
	if err := run("cmd", "/C", mtuSetupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", mtuSetupCmd, err)
	}

	return nil
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func AddRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	cmd := fmt.Sprintf(addRouteCMDFmt, ip, netmask, gateway)
	fmt.Fprintf(os.Stdout, "Running command: \"%s\"\n", cmd)
	return run("cmd", "/C", cmd)
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func DeleteRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	cmd := fmt.Sprintf(deleteRouteCMDFmt, ip, netmask, gateway)
	return run("cmd", "/C", cmd)
}
