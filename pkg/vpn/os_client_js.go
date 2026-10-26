//go:build js && wasm

// Package vpn pkg/vpn/os_client_js.go c2-app-vpn
//
// js/wasm client environment: no OS routing table, no privileges — but a
// REAL data plane, because the "TUN" here is a gVisor userspace netstack
// (tun_device_js.go). SetupTUN configures that stack with the server-assigned
// tunnel address, and the routing-table mutations the native client performs
// are inherently satisfied (the netstack's only route IS the tunnel), so they
// are successful no-ops rather than errors. The server side stays impossible
// in a browser (no clearnet egress to offer).
package vpn

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

var errNoTUNOnJS = errors.New("vpn: no TUN device on js/wasm — the VPN server cannot run in a browser")

// DefaultNetworkGateway has no routing table to read; the unspecified
// address keeps NewClient constructible (mirrors the Android build).
func DefaultNetworkGateway() (net.IP, error) {
	return net.IPv4zero, nil
}

// setupClientSysPrivileges is a no-op: there are no OS privileges in wasm.
func setupClientSysPrivileges() (int, error) {
	return 0, nil
}

func releaseClientSysPrivileges(_ int) error {
	return nil
}

// SetupTUN configures the netstack "device" with the server-assigned tunnel
// address — the js analogue of `ip addr add` + the default route.
func (c *Client) SetupTUN(ifcName, ipCIDR, _ string, mtu int) error {
	if c.cfg.Host {
		_, err := hostNetCall("setup", ifcName, ipCIDR, strconv.Itoa(mtu))
		return err
	}
	t, ok := c.tun.(*netstackTUN)
	if !ok {
		return fmt.Errorf("vpn: SetupTUN on js expects the netstack device, have %T", c.tun)
	}
	return t.configure(ipCIDR)
}

// ChangeRoute is a successful no-op for the netstack, whose only route is
// the tunnel. A host tunnel changes the host's route.
func (c *Client) ChangeRoute(ip, gateway string) error { return c.routeOnHost("change", ip, gateway) }

// AddRoute is a successful no-op for the netstack. A host tunnel adds the
// route on the host.
func (c *Client) AddRoute(ip, gateway string) error { return c.routeOnHost("add", ip, gateway) }

// DeleteRoute is a successful no-op for the netstack. A host tunnel deletes
// the route on the host.
func (c *Client) DeleteRoute(ip, gateway string) error { return c.routeOnHost("del", ip, gateway) }

func (c *Client) routeOnHost(op, ip, gateway string) error {
	if !c.cfg.Host {
		return nil
	}
	_, err := hostNetCall("route", op, ip, gateway)
	return err
}

// RevertDNS is a no-op: no resolver was ever changed.
func (c *Client) RevertDNS() {}

// SetupTUN (server side) always fails: no TUN in a browser.
func (s *Server) SetupTUN(_, _, _ string, _ int) error { return errNoTUNOnJS }

// AddRoute (server side) is unreachable without a TUN.
func (s *Server) AddRoute(_, _ string) error { return errNoTUNOnJS }
