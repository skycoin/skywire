//go:build darwin
// +build darwin

// Package netutil pkg/netutil/net_darwin.go c0-com-util
package netutil

import (
	"bytes"
	"fmt"
	"os/exec"
)

const (
	// Match the "interface:" line by label rather than a fixed line number:
	// `route -n get default` omits the gateway line for link-scoped default
	// routes (e.g. a Tailscale/utun default), which shifts every line up, so a
	// hardcoded FNR would grab the flags line instead of the interface name.
	defaultNetworkInterfaceCMD = "route -n get default | awk '/interface:/{print $2}'"
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
