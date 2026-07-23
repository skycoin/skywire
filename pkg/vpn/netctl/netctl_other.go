//go:build !linux
// +build !linux

// Package netctl pkg/vpn/netctl/netctl_other.go c4-app-vpn
//
// netctl is Linux-only (netlink); these stubs keep the package building on other
// platforms (skywire cross-compiles for darwin/windows). Nothing outside the
// linux-tagged VPN files imports netctl, so these are never called.
package netctl

import "errors"

var errUnsupported = errors.New("netctl: only supported on Linux")

// FlushAddrs is unsupported off Linux.
func FlushAddrs(string) error { return errUnsupported }

// AddrAdd is unsupported off Linux.
func AddrAdd(string, string) error { return errUnsupported }

// AddrDel is unsupported off Linux.
func AddrDel(string, string) error { return errUnsupported }

// LinkUp is unsupported off Linux.
func LinkUp(string) error { return errUnsupported }

// SetMTU is unsupported off Linux.
func SetMTU(string, int) error { return errUnsupported }

// RouteReplaceViaGateway is unsupported off Linux.
func RouteReplaceViaGateway(string, string) error { return errUnsupported }

// RouteAddViaGateway is unsupported off Linux.
func RouteAddViaGateway(string, string) error { return errUnsupported }

// RouteDelViaGateway is unsupported off Linux.
func RouteDelViaGateway(string, string) error { return errUnsupported }

// ReplaceDefaultRouteDev is unsupported off Linux.
func ReplaceDefaultRouteDev(string, int) error { return errUnsupported }

// FlushTable is unsupported off Linux.
func FlushTable(int) error { return errUnsupported }

// AddRuleIif is unsupported off Linux.
func AddRuleIif(string, int) error { return errUnsupported }

// DelRuleIif is unsupported off Linux.
func DelRuleIif(string, int) error { return errUnsupported }

// GetIPv4Forwarding is unsupported off Linux.
func GetIPv4Forwarding() (string, error) { return "", errUnsupported }

// SetIPv4Forwarding is unsupported off Linux.
func SetIPv4Forwarding(string) error { return errUnsupported }

// GetIPv6Forwarding is unsupported off Linux.
func GetIPv6Forwarding() (string, error) { return "", errUnsupported }

// SetIPv6Forwarding is unsupported off Linux.
func SetIPv6Forwarding(string) error { return errUnsupported }
