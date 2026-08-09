//go:build linux
// +build linux

// Package netutil pkg/netutil/net_linux.go c0-com-util
package netutil

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"

	"github.com/wlynxg/anet"
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
	if name := string(outputBytes); name != "" {
		return name, nil
	}

	// Android satisfies the linux build tag but gives an app process no
	// routing table to read, so `ip r` prints nothing and exits 0. Fall back
	// to the first usable interface. When none qualifies (airplane mode),
	// return "" without error — the historical contract of this function,
	// which callers handle as "unknown" — rather than failing summaries.
	return firstRoutableInterface()
}

func firstRoutableInterface() (string, error) {
	ifaces, err := anet.Interfaces()
	if err != nil {
		return "", fmt.Errorf("error getting network interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if IsVirtualInterface(iface.Name) {
			continue
		}
		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() {
				return iface.Name, nil
			}
		}
	}
	return "", nil
}
