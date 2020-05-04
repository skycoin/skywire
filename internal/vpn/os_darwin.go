//+build darwin

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
	gatewayForIfcCMDFmt     = "netstat -rn | grep default | grep %s | awk '{print $2}'"
	setIPv4ForwardingCMDFmt = "sysctl -w net.inet.ip.forwarding=%s"
	setIPv6ForwardingCMDFmt = "sysctl -w net.inet6.ip6.forwarding=%s"
	getIPv4ForwardingCMD    = "sysctl net.inet.ip.forwarding"
	getIPv6ForwardingCMD    = "sysctl net.inet6.ip6.forwarding"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return run("ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	defaultNetworkIfcName, err := DefaultNetworkInterface()
	if err != nil {
		return nil, fmt.Errorf("error getting default network interface name: %w", err)
	}

	return networkInterfaceGateway(defaultNetworkIfcName)
}

// EnableIPMasquerading enables IP masquerading for the interface with name `ifcName`.
func EnableIPMasquerading(_ string) error {
	return errors.New("cannot be implemented")
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(_ string) error {
	return errors.New("cannot be implemented")
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func AddRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return run("route", "add", "-net", ip, gateway, netmask)
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func DeleteRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	return run("route", "delete", "-net", ip, gateway, netmask)
}

// networkInterfaceGateway gets gateway of the network interface with name `ifcName`.
func networkInterfaceGateway(ifcName string) (net.IP, error) {
	cmd := fmt.Sprintf(gatewayForIfcCMDFmt, ifcName)
	outBytes, err := exec.Command("sh", "-c", cmd).Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("error running command %s: %w", cmd, err)
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

	return nil, fmt.Errorf("couldn't find gateway IP for \"%s\"", ifcName)
}

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{':'})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
