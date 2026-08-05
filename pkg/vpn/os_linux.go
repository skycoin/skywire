//go:build linux
// +build linux

// Package vpn pkg/vpn/os_linux.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

// mapRouteAddErr reproduces the old `ip route add` stderr handling for the
// netlink path: a pre-existing route (EEXIST) is not an error, and a permission
// failure (EPERM) surfaces as errPermissionDenied.
func mapRouteAddErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return nil
	case errors.Is(err, unix.EPERM):
		return errPermissionDenied
	default:
		return err
	}
}

// The client's half of this — SetupTUN and the route/DNS calls — lives in
// os_client_linux.go, which Android replaces wholesale (os_client_android.go):
// there the routing table belongs to VpnService, not to us.

// Server

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func (s *Server) SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	if err := netctl.AddrAdd(ifcName, ipCIDR); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}

	if err := netctl.SetMTU(ifcName, mtu); err != nil {
		return fmt.Errorf("error setting MTU: %w", err)
	}

	ip, _, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	if err := netctl.LinkUp(ifcName); err != nil {
		return fmt.Errorf("error setting interface up: %w", err)
	}

	if err := s.AddRoute(ip, gateway); err != nil {
		return fmt.Errorf("error setting gateway for interface: %w", err)
	}

	return nil
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func (s *Server) AddRoute(ip, gateway string) error {
	return mapRouteAddErr(netctl.RouteAddViaGateway(ip, gateway))
}
