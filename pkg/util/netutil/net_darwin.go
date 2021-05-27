//+build darwin

package netutil

import (
	"bytes"
	"fmt"
	"os/exec"
)

const (
	defaultNetworkInterfaceCMD = "route -n get default | awk 'FNR == 5 {print $2}'"
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
