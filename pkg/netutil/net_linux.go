//go:build linux
// +build linux

// Package netutil pkg/netutil/net_linux.go
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

	return string(outputBytes), nil
}
