//+build linux

package vpn

import (
	"bytes"
	"fmt"
	"os/exec"
)

const (
	gatewayForIfcCMDFmt         = "route -n | grep %s | awk '$1 == \"0.0.0.0\" {print $2}'"
	setIPv4ForwardingCMDFmt     = "sysctl -w net.ipv4.ip_forward=%s"
	setIPv6ForwardingCMDFmt     = "sysctl -w net.ipv6.conf.all.forwarding=%s"
	getIPv4ForwardingCMD        = "sysctl net.ipv4.ip_forward"
	getIPv6ForwardingCMD        = "sysctl net.ipv6.conf.all.forwarding"
	enableIPMasqueradingCMDFmt  = "iptables -t nat -A POSTROUTING -o %s -j MASQUERADE"
	disableIPMasqueradingCMDFmt = "iptables -t nat -D POSTROUTING -o %s -j MASQUERADE"
)

// EnableIPMasquerading enables IP masquerading for the interface with name `ifcName`.
func EnableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(enableIPMasqueradingCMDFmt, ifcName)
	// nolint:gosec
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(disableIPMasqueradingCMDFmt, ifcName)
	// nolint:gosec
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func AddRoute(ip, gateway, netmask string) error {
	if netmask == "" {
		netmask = "255.255.255.255"
	}

	return run("route", "add", "-net", ip, "netmask", netmask, "gw", gateway)
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func DeleteRoute(ip, gateway, netmask string) error {
	if netmask == "" {
		netmask = "255.255.255.255"
	}

	return run("route", "delete", "-net", ip, "netmask", netmask, "gw", gateway)
}

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{'='})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
