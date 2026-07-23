//go:build linux
// +build linux

// Package vpn pkg/vpn/firewall_linux.go c4-app-vpn
//
// firewall abstracts the vpn-router's packet-filter / NAT rules behind a small
// interface with two build-tagged backends, so the SAME router code runs either
// with iptables (default — the fleet's proven path) or with a pure-Go nftables
// backend (build tag `nftfw`, for the gokrazy appliance, which ships no iptables
// binary). Each backend tracks the rules it installs and removes them all in
// teardown(). See firewall_iptables_linux.go and firewall_nftfw_linux.go.
package vpn

import "net"

// fwHook selects which NAT chain a REDIRECT lands in.
type fwHook int

const (
	// fwPrerouting redirects forwarded/inbound traffic (the router/LAN case).
	fwPrerouting fwHook = iota
	// fwOutput redirects locally-generated traffic (the single-host case).
	fwOutput
)

// firewall is the router's view of its packet-filter/NAT rules.
type firewall interface {
	// masquerade SNATs traffic leaving oif.
	masquerade(oif string) error
	// forwardAccept accepts iif→oif forwarded traffic; stateful additionally
	// requires ct state related,established (the return path).
	forwardAccept(iif, oif string, stateful bool) error
	// clampMSS clamps forwarded TCP SYNs' MSS to the path MTU on oif.
	clampMSS(oif string) error
	// redirectTCP redirects TCP for dst (a CIDR) to a local port; iif optional.
	redirectTCP(hook fwHook, iif string, dst *net.IPNet, toPort uint16) error
	// teardown removes every rule this firewall installed.
	teardown()
}
