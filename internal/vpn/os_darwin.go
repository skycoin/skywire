//+build darwin

package vpn

import (
	"bytes"
	"errors"
	"fmt"
)

const (
	gatewayForIfcCMDFmt     = "netstat -rn | grep default | grep %s | awk '{print $2}'"
	setIPv4ForwardingCMDFmt = "sysctl -w net.inet.ip.forwarding=%s"
	setIPv6ForwardingCMDFmt = "sysctl -w net.inet6.ip6.forwarding=%s"
	getIPv4ForwardingCMD    = "sysctl net.inet.ip.forwarding"
	getIPv6ForwardingCMD    = "sysctl net.inet6.ip6.forwarding"
)

// EnableIPMasquerading enables IP masquerading for the interface with name `ifcName`.
func EnableIPMasquerading(_ string) error {
	return errors.New("cannot be implemented")
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(_ string) error {
	return errors.New("cannot be implemented")
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func AddRoute(ip, gateway, netmask string) error {
	if netmask == "" {
		return run("route", "add", "-net", ip, gateway)
	}

	return run("route", "add", "-net", ip, gateway, netmask)
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func DeleteRoute(ip, gateway, netmask string) error {
	if netmask == "" {
		return run("route", "delete", "-net", ip, gateway)
	}

	return run("route", "delete", "-net", ip, gateway, netmask)
}

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{':'})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
