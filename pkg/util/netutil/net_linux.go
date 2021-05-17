//+build linux

package netutil

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
)

const (
	defaultNetworkInterfaceCMD = "ip r | awk '$1 == \"default\" {print $5}'"
)

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	outputBytes, err := exec.Command("sh", "-c", defaultNetworkInterfaceCMD).Output()
	if err != nil {
		return "", fmt.Errorf("error running command %s: %w", defaultNetworkInterfaceCMD, err)
	}

	// just in case
	outputBytes = bytes.TrimRight(outputBytes, "\n")

	return string(outputBytes), nil
}

// DefaultNetworkInterfaceIPs returns IP addresses for the default network interface
func DefaultNetworkInterfaceIPs() ([]net.IP, error) {
	networkIfc, err := DefaultNetworkInterface()
	if err != nil {
		return nil, fmt.Errorf("failed to get default network interface: %w", err)
	}
	localIPs, err := NetworkInterfaceIPs(networkIfc)
	if err != nil {
		return nil, fmt.Errorf("failed to get IPs of %s: %w", networkIfc, err)
	}
	return localIPs, nil
}
