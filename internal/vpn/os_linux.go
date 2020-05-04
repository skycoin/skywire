//+build linux

package vpn

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
)

const (
	defaultNetworkGatewayCMD    = "ip r | grep \"default via\" | awk '{print $3}'"
	setIPv4ForwardingCMDFmt     = "sysctl -w net.ipv4.ip_forward=%s"
	setIPv6ForwardingCMDFmt     = "sysctl -w net.ipv6.conf.all.forwarding=%s"
	getIPv4ForwardingCMD        = "sysctl net.ipv4.ip_forward"
	getIPv6ForwardingCMD        = "sysctl net.ipv6.conf.all.forwarding"
	enableIPMasqueradingCMDFmt  = "iptables -t nat -A POSTROUTING -o %s -j MASQUERADE"
	disableIPMasqueradingCMDFmt = "iptables -t nat -D POSTROUTING -o %s -j MASQUERADE"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	if err := run("ip", "a", "add", ipCIDR, "dev", ifcName); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}

	if err := run("ip", "link", "set", "dev", ifcName, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("error setting MTU: %w", err)
	}

	ip, _, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	if err := run("ip", "link", "set", ifcName, "up"); err != nil {
		return fmt.Errorf("error setting interface up: %w", err)
	}

	if err := AddRoute(ip, gateway); err != nil {
		return fmt.Errorf("error setting gateway for interface: %w", err)
	}

	return nil
}

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	outBytes, err := exec.Command("sh", "-c", defaultNetworkGatewayCMD).Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("error running command %s: %w", defaultNetworkGatewayCMD, err)
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

	return nil, errors.New("couldn't find default network gateway")
}

// EnableIPMasquerading enables IP masquerading for the interface with name `ifcName`.
func EnableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(enableIPMasqueradingCMDFmt, ifcName)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(disableIPMasqueradingCMDFmt, ifcName)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func AddRoute(ip, gateway string) error {
	return run("ip", "r", "add", ip, "via", gateway)
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func DeleteRoute(ip, gateway string) error {
	return run("ip", "r", "del", ip, "via", gateway)
}

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{'='})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
