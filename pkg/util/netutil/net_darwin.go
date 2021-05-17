//+build darwin

package netutil

import (
	"bytes"
	"fmt"
	"os/exec"
)

const (
	defaultNetworkInterfaceCMD = "netstat -rn | sed -n '/Internet/,/Internet6/p' | grep default | awk '{print $4}'"
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
