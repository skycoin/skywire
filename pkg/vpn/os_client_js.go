//go:build js && wasm

// Package vpn pkg/vpn/os_client_js.go c2-app-vpn
//
// js/wasm stand-ins: a browser has no TUN device, no routing table, and no
// privileges to acquire. These exist so the vpn-client package — and every
// command that shows its help — compiles in the wasm build of the full
// skywire binary; starting a VPN client there fails cleanly at the TUN step.
package vpn

import (
	"errors"
	"net"
)

var errNoTUNOnJS = errors.New("vpn: no TUN device on js/wasm — the VPN client cannot run in a browser")

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

// SetupTUN always fails: nothing can provide a TUN in a browser.
func (c *Client) SetupTUN(_, _, _ string, _ int) error {
	return errNoTUNOnJS
}

// ChangeRoute is unreachable without a TUN.
func (c *Client) ChangeRoute(_, _ string) error { return errNoTUNOnJS }

// AddRoute is unreachable without a TUN.
func (c *Client) AddRoute(_, _ string) error { return errNoTUNOnJS }

// DeleteRoute is unreachable without a TUN.
func (c *Client) DeleteRoute(_, _ string) error { return errNoTUNOnJS }

// RevertDNS is a no-op: no resolver was ever changed.
func (c *Client) RevertDNS() {}

// SetupTUN (server side) always fails: no TUN in a browser.
func (s *Server) SetupTUN(_, _, _ string, _ int) error { return errNoTUNOnJS }

// AddRoute (server side) is unreachable without a TUN.
func (s *Server) AddRoute(_, _ string) error { return errNoTUNOnJS }
