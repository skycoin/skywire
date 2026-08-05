//go:build linux && !android
// +build linux,!android

// Package vpn pkg/vpn/os_client_linux.go c4-app-vpn
package vpn

import (
	"bytes"
	"fmt"
	"net"
	"time"

	"github.com/syndtr/gocapability/capability"
	"golang.org/x/sys/unix"

	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

const (
	defaultNetworkGatewayCMD = `ip r | grep "default via" | awk '{print $3}'`
)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	outBytes, err := osutil.RunWithResult("sh", "-c", defaultNetworkGatewayCMD)
	if err != nil {
		return nil, err
	}

	outBytes = bytes.TrimRight(outBytes, "\n")

	outLines := bytes.Split(outBytes, []byte{'\n'})

	for _, l := range outLines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			// initially look for IPv4 address
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, errCouldFindDefaultNetworkGateway
}

func setupClientSysPrivileges() (int, error) {
	var err error

	var caps capability.Capabilities

	caps, err = capability.NewPid2(0)
	if err != nil {
		err = fmt.Errorf("failed to init capabilities: %w", err)
		return 0, err
	}

	err = caps.Load()
	if err != nil {
		err = fmt.Errorf("failed to load capabilities: %w", err)
		return 0, err
	}

	// set `CAP_NET_ADMIN` capability to needed caps sets.
	caps.Set(capability.CAPS|capability.BOUNDS|capability.AMBIENT, capability.CAP_NET_ADMIN)
	err = caps.Apply(capability.CAPS | capability.BOUNDS | capability.AMBIENT)
	if err != nil {
		err = fmt.Errorf("failed to apply capabilties: %w", err)
		return 0, err
	}

	// let child process keep caps sets from the parent, so we may do calls to
	// system utilities with these caps.
	err = unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0)
	if err != nil {
		err = fmt.Errorf("failed to set PR_SET_KEEPCAPS: %w", err)
		return 0, err
	}

	return 0, nil
}

func releaseClientSysPrivileges(_ int) error {
	return nil
}

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (c *Client) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for SetupTUN: %v\n", err))
		return err
	}
	if err := netctl.AddrAdd(ifcName, ipCIDR); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}
	c.releaseSysPrivileges()

	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for SetupTUN: %v\n", err))
		return err
	}
	if err := netctl.SetMTU(ifcName, mtu); err != nil {
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
	if err := netctl.LinkUp(ifcName); err != nil {
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
	return netctl.RouteReplaceViaGateway(ip, gateway)
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func (c *Client) AddRoute(ip, gateway string) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for AddRoute: %v\n", err))
		return err
	}
	defer c.releaseSysPrivileges()
	return mapRouteAddErr(netctl.RouteAddViaGateway(ip, gateway))
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func (c *Client) DeleteRoute(ip, gateway string) error {
	if err := c.setSysPrivileges(); err != nil {
		print(fmt.Sprintf("Failed to setup system privileges for DeleteRoute: %v\n", err))
		return err
	}
	defer c.releaseSysPrivileges()
	return netctl.RouteDelViaGateway(ip, gateway)
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
