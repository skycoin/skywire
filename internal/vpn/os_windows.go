//go:build windows
// +build windows

package vpn

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	tunSetupCMDFmt    = "netsh interface ip set address name=\"%s\" source=static addr=%s mask=%s gateway=%s"
	tunMTUSetupCMDFmt = "netsh interface ipv4 set subinterface \"%s\" mtu=%d"
	tunDNSCMDFmt      = "netsh interface ip set dns \"%s\" static %s"
	modifyRouteCMDFmt = "route %s %s mask %s %s"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (c *Client) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	setupCmd := fmt.Sprintf(tunSetupCMDFmt, ifcName, ip, netmask, gateway)
	if err := osutil.Run("cmd", "/C", setupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", setupCmd, err)
	}

	mtuSetupCmd := fmt.Sprintf(tunMTUSetupCMDFmt, ifcName, mtu)
	if err := osutil.Run("cmd", "/C", mtuSetupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", mtuSetupCmd, err)
	}

	if c.cfg.DNSAddr != "" {
		c.SetupDNS()
	}

	return nil
}

// SetupDNS trying to set DNS server
func (c *Client) SetupDNS() {
	dnsSetupCmd := fmt.Sprintf(tunDNSCMDFmt, c.tun.Name(), c.cfg.DNSAddr)
	if _, err := osutil.RunWithResult("cmd", "/C", dnsSetupCmd); err != nil {
		fmt.Printf("Failed to setup DNS. Continue with machine default DNS setting: %s\n", err)
	} else {
		fmt.Printf("DNS setup successful: %s\n", c.cfg.DNSAddr)
	}
}

// RevertDNS trying to revert DNS values same as before starting vpn-client if it changed
func (c *Client) RevertDNS() {
	if c.cfg.DNSAddr != "" {
		dnsRevertCmd := fmt.Sprintf(tunDNSCMDFmt, c.tun.Name(), "none")
		osutil.RunWithResult("cmd", "/C", dnsRevertCmd) //nolint
	}
}

// ChangeRoute changes current route to `ipCIDR` to go through the `gateway`
// in the OS routing table.
func (c *Client) ChangeRoute(ipCIDR, gateway string) error {
	return modifyRoutingTable("change", ipCIDR, gateway)
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func (c *Client) AddRoute(ipCIDR, gateway string) error {
	return modifyRoutingTable("add", ipCIDR, gateway)
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func (c *Client) DeleteRoute(ipCIDR, gateway string) error {
	return modifyRoutingTable("delete", ipCIDR, gateway)
}

func modifyRoutingTable(action, ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	cmd := fmt.Sprintf(modifyRouteCMDFmt, action, ip, netmask, gateway)
	err = osutil.Run("cmd", "/C", cmd)
	if err != nil {
		return errPermissionDenied
	}
	return nil
}

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (s *Server) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	setupCmd := fmt.Sprintf(tunSetupCMDFmt, ifcName, ip, netmask, gateway)
	if err := osutil.Run("cmd", "/C", setupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", setupCmd, err)
	}

	mtuSetupCmd := fmt.Sprintf(tunMTUSetupCMDFmt, ifcName, mtu)
	if err := osutil.Run("cmd", "/C", mtuSetupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", mtuSetupCmd, err)
	}

	return nil
}
