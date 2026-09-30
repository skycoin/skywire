//go:build linux
// +build linux

// Package netutil pkg/netutil/net_linux.go c0-com-util
package netutil

import (
	"bytes"
	"fmt"
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
