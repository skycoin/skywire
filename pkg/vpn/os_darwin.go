//go:build darwin

// Package vpn pkg/vpn/os_darwin.go c3-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (c *Client) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}
	if err := c.setSysPrivileges(); err != nil {
		c.logger().WithError(err).Error("Failed to setup system privileges for SetupTUN")
		return err
	}
	defer c.releaseSysPrivileges()
	if c.cfg.DNSAddr != "" {
		c.SetupDNS()
	}
	c.logger().Debugf("Default system DNS: %s", c.defaultSystemDNS)
	return osutil.Run("ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

// SetupDNS trying to set DNS server
func (c *Client) SetupDNS() {
	defaultDNSByte, err := osutil.RunWithResult("networksetup", "-getdnsservers", "Wi-Fi")
	if err != nil {
		c.logger().WithError(err).Warn("Failed to get current DNS servers")
	}
	c.defaultSystemDNS = string(defaultDNSByte)

	err = osutil.Run("networksetup", "-setdnsservers", "Wi-Fi", c.cfg.DNSAddr)
	if err != nil {
		c.logger().WithError(err).Warn("Failed to setup DNS. Continue with machine default DNS setting")
	} else {
		c.logger().Infof("DNS setup successful: %s", c.cfg.DNSAddr)
	}
}

// RevertDNS trying to revert DNS values same as before starting vpn-client if it changed
func (c *Client) RevertDNS() {
	if c.cfg.DNSAddr != "" {
		if err := osutil.Run("networksetup", "-setdnsservers", "Wi-Fi", c.defaultSystemDNS); err != nil {
			c.logger().WithError(err).Warn("Failed to revert DNS")
		} else {
			c.logger().Infof("System DNS value reverted back to %s", c.defaultSystemDNS)
		}
	}
}

// ChangeRoute changes current route to `ipCIDR` to go through the `gateway`
// in the OS routing table.
func (c *Client) ChangeRoute(ipCIDR, gateway string) error {
	return c.modifyRoutingTable("change", ipCIDR, gateway)
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func (c *Client) AddRoute(ipCIDR, gateway string) error {
	if err := c.modifyRoutingTable("add", ipCIDR, gateway); err != nil {
		if e, ok := errors.AsType[*osutil.ErrorWithStderr](err); ok {
			if strings.Contains(string(e.Stderr), "File exists") {
				return nil
			}
		}
		return err
	}
	return nil
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func (c *Client) DeleteRoute(ipCIDR, gateway string) error {
	return c.modifyRoutingTable("delete", ipCIDR, gateway)
}

func (c *Client) modifyRoutingTable(action, ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	if err := c.setSysPrivileges(); err != nil {
		c.logger().WithError(err).Errorf("Failed to setup system privileges for %s", action)
		return err
	}
	defer c.releaseSysPrivileges()
	return osutil.Run("route", action, "-net", ip, gateway, netmask)
}

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (s *Server) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return osutil.Run("ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}
