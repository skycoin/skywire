// Package router7 vendors the pure-Go DHCPv4 + DNS server libraries from
// router7 (https://github.com/rtr7/router7, Apache-2.0 — see LICENSE),
// decoupled from its Gokrazy appliance runtime so they run under a normal
// host OS. Only the LAN-serving slice is vendored (dhcp4d, dns, and their
// support packages); the appliance/WAN daemons and the heavy
// netconfig -> nftables/netlink/wireguard tree are omitted (the DHCP server's
// address is read from the live interface via the standard library instead).
//
// These replace the external dnsmasq the vpn-router app currently shells out
// to: pure Go, embeddable in the visor process. See 0magnet/router7's
// feat/host-os-standalone branch for the upstream-tracking standalone port
// this was copied from.
package router7
