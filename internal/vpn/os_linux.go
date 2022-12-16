//go:build linux
// +build linux

package vpn

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// Client

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (c *Client) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for SetupTUN: %v\n", err))
		return err
	}
	if err := osutil.Run("ip", "a", "add", ipCIDR, "dev", ifcName); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}
	c.releaseSysPrivileges()

	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for SetupTUN: %v\n", err))
		return err
	}
	if err := osutil.Run("ip", "link", "set", "dev", ifcName, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("error setting MTU: %w", err)
	}
	c.releaseSysPrivileges()

	ip, _, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for SetupTUN: %v\n", err))
		return err
	}
	if err := osutil.Run("ip", "link", "set", ifcName, "up"); err != nil {
		return fmt.Errorf("error setting interface up: %w", err)
	}
	c.releaseSysPrivileges()
	if c.cfg.DNSAddr != "" {
		if err := c.SetupDNS(); err != nil {
			fmt.Printf("error setting dns for interface: %s", err)
		}
	}

	// TODO (mrpalide): due to nmcli functionality, we should wait for reload network manager after use it for set DNS, then add routes
	// if we skip this stop here for a little (5) seconds, we lost all routes that will add by ip command
	// also we should fix it later, when nmcli guys add --preserved-external-ip flag due to this command:
	// https://gitlab.freedesktop.org/NetworkManager/NetworkManager/-/issues/1167#note_1690288
	time.Sleep(5 * time.Second)

	if err := c.AddRoute(ip, gateway); err != nil {
		return fmt.Errorf("error setting gateway for interface: %w", err)
	}

	return nil
}

// ChangeRoute changes current route to `ip` to go through the `gateway`
// in the OS routing table.
func (c *Client) ChangeRoute(ip, gateway string) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for ChangeRoute: %v\n", err))
		return err
	}
	defer c.releaseSysPrivileges()
	return osutil.Run("ip", "r", "change", ip, "via", gateway)
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func (c *Client) AddRoute(ip, gateway string) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for AddRoute: %v\n", err))
		return err
	}
	defer c.releaseSysPrivileges()
	err := osutil.Run("ip", "r", "add", ip, "via", gateway)

	var e *osutil.ErrorWithStderr
	if errors.As(err, &e) {
		if strings.Contains(string(e.Stderr), "File exists") {
			return nil
		}
	}

	if errors.As(err, &e) {
		if strings.Contains(string(e.Stderr), "Operation not permitted") {
			return errPermissionDenied
		}
	}

	return err
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func (c *Client) DeleteRoute(ip, gateway string) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for DeleteRoute: %v\n", err))
		return err
	}
	defer c.releaseSysPrivileges()
	return osutil.Run("ip", "r", "del", ip, "via", gateway)
}

// SetupDNS set dns address for TUN device on tun0
func (c *Client) SetupDNS() error {
	fmt.Printf("Set DNS on TUN %s\n", c.tun.Name())
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for AddDNS: %v\n", err))
		return err
	}
	err := osutil.Run("nmcli", "dev", "mod", c.tun.Name(), "+ipv4.dns", c.cfg.DNSAddr)
	c.releaseSysPrivileges()

	return err
}

// RevertDNS trying to revert DNS values same as before starting vpn-client if it changed
func (c *Client) RevertDNS() {
	if c.cfg.DNSAddr != "" {
		if err := c.setSysPrivileges(); err != nil {
			print(fmt.Sprintf("Failed to setup system privileges for RevertDNS: %v\n", err))
			return
		}
		err := osutil.Run("nmcli", "dev", "mod", c.tun.Name(), "-ipv4.dns", "0")
		if err != nil {
			print(fmt.Sprintf("Failed to revert DNS: %v\n", err))
		}
		c.releaseSysPrivileges()
	}
}

// Server

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (s *Server) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
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

	if err := s.AddRoute(ip, gateway); err != nil {
		return fmt.Errorf("error setting gateway for interface: %w", err)
	}

	return nil
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func (s *Server) AddRoute(ip, gateway string) error {
	err := osutil.Run("ip", "r", "add", ip, "via", gateway)

	var e *osutil.ErrorWithStderr
	if errors.As(err, &e) {
		if strings.Contains(string(e.Stderr), "File exists") {
			return nil
		}
	}

	if errors.As(err, &e) {
		if strings.Contains(string(e.Stderr), "Operation not permitted") {
			return errPermissionDenied
		}
	}

	return err
}
